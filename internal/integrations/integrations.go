// Package integrations connects a workspace to other systems (Settings → Integrations):
//
//   - CRM connectors (HubSpot, Salesforce) pull contacts on a schedule through a field mapping and
//     push a contact on demand (the API, or the journey step push_integration).
//   - Shopify posts store webhooks (orders, customers, checkouts), verified with Shopify's HMAC and
//     turned into Pulse events on identified contacts.
//   - REST hooks (Zapier, Make): the connector has its own API key; subscriptions are outbound
//     webhooks owned by the connector, and action endpoints create contacts, track events and tag.
//   - Ad audiences (Facebook Custom Audiences, Google Ads Customer Match) receive the hashed email
//     and phone of a linked segment's members, kept in step as membership changes (audience.go).
//
// Every remote base URL is a setting with the real one as default, so tests point them at mocks.
// Outbound calls go through the SSRF-safe client (internal/safehttp). Secrets are sealed with the
// server key and only ever shown masked.
package integrations

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"pulse/internal/attr"
	"pulse/internal/consent"
	"pulse/internal/event"
)

// Connector kinds.
const (
	KindHubSpot    = "hubspot"
	KindSalesforce = "salesforce"
	KindShopify    = "shopify"
	KindRESTHooks  = "rest_hooks"
	KindFacebook   = "facebook_audiences"
	KindGoogleAds  = "google_ads"
)

// Field is one setting in a connector's form.
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Hint        string `json:"hint,omitempty"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required"`
	// Hosts pins a URL setting to the vendor's hosts: an exact name, or ".suffix" for any subdomain.
	// Credentials are sent there, so other hosts are allowed only in local development.
	Hosts []string `json:"-"`
}

// Kind is one connector type in the catalog the web app renders.
type Kind struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Category    string     `json:"category"` // crm | ecommerce | automation | ads
	Description string     `json:"description"`
	Config      []Field    `json:"config"`
	Secrets     []Field    `json:"secrets"`
	Pull        bool       `json:"pull"`      // contacts are pulled on a schedule
	Push        bool       `json:"push"`      // contacts can be pushed (API, journey step)
	Webhooks    bool       `json:"webhooks"`  // the platform posts to Pulse
	Audiences   bool       `json:"audiences"` // segments can be linked to audiences
	Hooks       bool       `json:"hooks"`     // REST hooks with the connector's own API key
	Mapping     []FieldMap `json:"default_mapping,omitempty"`
	RemoteID    string     `json:"remote_id_attr,omitempty"` // the attribute keeping the remote record id
}

// FieldMap maps one remote field to a Pulse attribute. Dir is "both" (default), "pull" (remote →
// Pulse only) or "push" (Pulse → remote only).
type FieldMap struct {
	Remote string `json:"remote"`
	Attr   string `json:"attr"`
	Dir    string `json:"dir,omitempty"`
}

func (m FieldMap) pulls() bool  { return m.Dir != "push" }
func (m FieldMap) pushes() bool { return m.Dir != "pull" }

var kinds = []Kind{
	{
		ID: KindHubSpot, Name: "HubSpot", Category: "crm",
		Description: "Pull HubSpot contacts on a schedule and push Pulse contacts to HubSpot. Uses a private app access token with the crm.objects.contacts read and write scopes.",
		Config: []Field{
			{Key: "base_url", Label: "API base URL", Default: "https://api.hubapi.com", Hint: "Change only for testing.", Hosts: []string{"api.hubapi.com", "api.hubspot.com"}},
		},
		Secrets: []Field{{Key: "access_token", Label: "Private app access token", Placeholder: "pat-na1-…", Required: true}},
		Pull:    true, Push: true, RemoteID: "hubspot_id",
		Mapping: []FieldMap{{"email", "email", "both"}, {"firstname", "first_name", "both"}, {"lastname", "last_name", "both"},
			{"phone", "phone", "both"}, {"city", "city", "both"}},
	},
	{
		ID: KindSalesforce, Name: "Salesforce", Category: "crm",
		Description: "Pull Salesforce contacts or leads on a schedule and push Pulse contacts to Salesforce. Uses a connected app with the OAuth client credentials flow.",
		Config: []Field{
			{Key: "login_url", Label: "My Domain URL", Placeholder: "https://yourcompany.my.salesforce.com", Required: true,
				Hint: "The client credentials flow needs your My Domain URL, not login.salesforce.com.", Hosts: []string{".salesforce.com"}},
			{Key: "object", Label: "Object", Default: "Contact", Hint: "Contact or Lead."},
			{Key: "api_version", Label: "API version", Default: "v60.0"},
		},
		Secrets: []Field{
			{Key: "client_id", Label: "Consumer key", Required: true},
			{Key: "client_secret", Label: "Consumer secret", Required: true},
		},
		Pull: true, Push: true, RemoteID: "salesforce_id",
		Mapping: []FieldMap{{"Email", "email", "both"}, {"FirstName", "first_name", "both"}, {"LastName", "last_name", "both"},
			{"Phone", "phone", "both"}, {"MailingCity", "city", "both"}},
	},
	{
		ID: KindShopify, Name: "Shopify", Category: "ecommerce",
		Description: "Turn Shopify orders, new customers and checkouts into Pulse events: order_completed (with revenue), customer_created and cart_updated for abandoned-cart journeys.",
		Config: []Field{
			{Key: "shop", Label: "Shop domain", Placeholder: "your-store.myshopify.com", Required: true},
			{Key: "base_url", Label: "Admin API base URL", Hint: "Empty uses https://<shop domain>. Change only for testing.", Hosts: []string{".myshopify.com"}},
			{Key: "api_version", Label: "Admin API version", Default: "2024-10"},
		},
		Secrets: []Field{
			{Key: "webhook_secret", Label: "Webhook signing secret", Required: true,
				Hint: "Shopify admin → Settings → Notifications → Webhooks shows it, or your app's client secret."},
			{Key: "access_token", Label: "Admin API access token", Placeholder: "shpat_…", Hint: "Only for Test connection."},
		},
		Webhooks: true, RemoteID: "shopify_customer_id",
	},
	{
		ID: KindRESTHooks, Name: "Zapier / Make", Category: "automation",
		Description: "REST hooks for Zapier, Make and similar tools: triggers when a contact is created, enters or leaves a segment, submits a form or does any event; actions create or update contacts, track events and add tags.",
		Hooks:       true,
	},
	{
		ID: KindFacebook, Name: "Facebook Custom Audiences", Category: "ads",
		Description: "Keep a Facebook (Meta) Custom Audience in step with a segment: hashed emails and phone numbers, updated as contacts enter and leave.",
		Config: []Field{
			{Key: "ad_account_id", Label: "Ad account ID", Placeholder: "act_1234567890 or 1234567890", Required: true},
			{Key: "default_country", Label: "Default country calling code", Placeholder: "91", Hint: "For phone numbers saved without +country."},
			{Key: "base_url", Label: "Graph API base URL", Default: "https://graph.facebook.com/v21.0", Hint: "Change only for testing.", Hosts: []string{"graph.facebook.com"}},
		},
		Secrets:   []Field{{Key: "access_token", Label: "System user access token", Required: true, Hint: "Needs ads_management on the ad account."}},
		Audiences: true,
	},
	{
		ID: KindGoogleAds, Name: "Google Ads Customer Match", Category: "ads",
		Description: "Keep a Google Ads Customer Match list in step with a segment: hashed emails and phone numbers, updated as contacts enter and leave.",
		Config: []Field{
			{Key: "customer_id", Label: "Customer ID", Placeholder: "123-456-7890", Required: true},
			{Key: "login_customer_id", Label: "Manager account ID", Hint: "When you reach the account through a manager account."},
			{Key: "default_country", Label: "Default country calling code", Placeholder: "91", Hint: "For phone numbers saved without +country."},
			{Key: "base_url", Label: "Google Ads API base URL", Default: "https://googleads.googleapis.com/v18", Hint: "Change only for testing.", Hosts: []string{"googleads.googleapis.com"}},
			{Key: "token_url", Label: "OAuth token URL", Default: "https://oauth2.googleapis.com/token", Hint: "Change only for testing.", Hosts: []string{"oauth2.googleapis.com"}},
		},
		Secrets: []Field{
			{Key: "developer_token", Label: "Developer token", Required: true},
			{Key: "client_id", Label: "OAuth client ID", Required: true},
			{Key: "client_secret", Label: "OAuth client secret", Required: true},
			{Key: "refresh_token", Label: "Refresh token", Required: true},
		},
		Audiences: true,
	},
}

// Catalog lists every connector kind. Config and Secrets are never nil, so the JSON always has
// arrays.
func Catalog() []Kind {
	out := make([]Kind, len(kinds))
	for i, k := range kinds {
		if k.Config == nil {
			k.Config = []Field{}
		}
		if k.Secrets == nil {
			k.Secrets = []Field{}
		}
		out[i] = k
	}
	return out
}

// Lookup finds a kind by id.
func Lookup(id string) (Kind, bool) {
	for _, k := range kinds {
		if k.ID == id {
			return k, true
		}
	}
	return Kind{}, false
}

// Connector is a configured integration of a workspace. Secrets are opened only inside the
// server; the API shows Masked.
type Connector struct {
	ID          int64             `json:"id"`
	TenantID    int64             `json:"-"`
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Config      map[string]string `json:"config"`
	Secrets     map[string]string `json:"-"`
	Masked      map[string]string `json:"secrets"`
	Sealed      []byte            `json:"-"`
	Mapping     []FieldMap        `json:"mapping"`
	Active      bool              `json:"active"`
	SyncMinutes int               `json:"sync_minutes"`
	NextSyncAt  *time.Time        `json:"next_sync_at,omitempty"`
	Cursor      string            `json:"cursor,omitempty"`
	Status      string            `json:"status"`
	LastError   string            `json:"last_error,omitempty"`
	LastSyncAt  *time.Time        `json:"last_sync_at,omitempty"`
	KeyHint     string            `json:"key_hint,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// Setting returns a config value, or the kind's default.
func (c Connector) Setting(key string) string {
	if v := strings.TrimSpace(c.Config[key]); v != "" {
		return v
	}
	k, _ := Lookup(c.Kind)
	for _, f := range k.Config {
		if f.Key == key {
			return f.Default
		}
	}
	return ""
}

// LogEntry is one line of a connector's sync log.
type LogEntry struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Action  string    `json:"action"` // pull | push | webhook | audience | reconcile | test
	OK      bool      `json:"ok"`
	Count   int       `json:"count"`
	Message string    `json:"message,omitempty"`
}

// Mask shows the last 4 characters of a secret (all dots when it's short).
func Mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••••••"
	}
	return "••••••••" + s[len(s)-4:]
}

// MaskAll masks every secret.
func MaskAll(secrets map[string]string) map[string]string {
	out := make(map[string]string, len(secrets))
	for k, v := range secrets {
		out[k] = Mask(v)
	}
	return out
}

// MergeSecrets applies an edit to stored secrets: a field left empty, or sent back masked, keeps
// the stored value.
func MergeSecrets(stored, edit map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range stored {
		out[k] = v
	}
	for k, v := range edit {
		v = strings.TrimSpace(v)
		if v == "" || strings.HasPrefix(v, "••••") {
			continue
		}
		out[k] = v
	}
	return out
}

// MappableAttr reports why attr can't receive pulled values ("" when it can). Reserved attributes
// change only through their events (consent, tags, points, stage, company, owner, dates, device).
func MappableAttr(a string) string {
	switch {
	case !attr.ValidKey(a):
		return fmt.Sprintf("%q isn't a valid attribute name (letters, digits and _)", a)
	case consent.IsReserved(a) || event.EventOnlyAttr(a) || strings.HasPrefix(a, "optout_"):
		return fmt.Sprintf("%q is kept by Pulse and changes only through its own events; map it as push only, or use another attribute", a)
	}
	return ""
}

// Validate checks a connector before it's saved; secrets are the merged (stored + edited) ones.
// CheckHosts refuses URL settings outside their vendor's hosts (see Field.Hosts), and plain http,
// unless anyHost (local development, PULSE_ALLOW_PRIVATE_WEBHOOKS) allows test servers.
func CheckHosts(c Connector, anyHost bool) error {
	k, ok := Lookup(c.Kind)
	if !ok || anyHost {
		return nil
	}
	for _, f := range k.Config {
		v := strings.TrimSpace(c.Config[f.Key])
		if v == "" || len(f.Hosts) == 0 {
			continue
		}
		u, err := url.Parse(v)
		if err != nil || u.Scheme != "https" || !HostAllowed(u.Hostname(), f.Hosts) {
			return fmt.Errorf("%s must be an https URL on %s", f.Label, strings.Join(f.Hosts, " or "))
		}
	}
	return nil
}

// HostAllowed matches host against exact names and ".suffix" patterns.
func HostAllowed(host string, hosts []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, h := range hosts {
		if strings.HasPrefix(h, ".") && strings.HasSuffix(host, h) && len(host) > len(h) || host == h {
			return true
		}
	}
	return false
}

// EndpointsChanged reports whether any setting that decides where credentials go (URLs, the shop)
// differs between two configs: stored secrets must then be entered again.
func EndpointsChanged(before, after map[string]string) bool {
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	for k := range keys {
		if (strings.HasSuffix(k, "_url") || k == "shop") && strings.TrimSpace(before[k]) != strings.TrimSpace(after[k]) {
			return true
		}
	}
	return false
}

func Validate(c Connector, secrets map[string]string) error {
	k, ok := Lookup(c.Kind)
	if !ok {
		return errors.New("unknown integration type")
	}
	var errs []error
	if strings.TrimSpace(c.Name) == "" {
		errs = append(errs, errors.New("name is required"))
	}
	for _, f := range k.Config {
		v := strings.TrimSpace(c.Config[f.Key])
		if f.Required && v == "" {
			errs = append(errs, fmt.Errorf("%s is required", f.Label))
		}
		if v != "" && (strings.HasSuffix(f.Key, "_url")) {
			if u, err := url.Parse(v); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				errs = append(errs, fmt.Errorf("%s must be an http(s) URL", f.Label))
			}
		}
	}
	for key := range c.Config {
		if !slices.ContainsFunc(k.Config, func(f Field) bool { return f.Key == key }) {
			errs = append(errs, fmt.Errorf("unknown setting %q", key))
		}
	}
	for _, f := range k.Secrets {
		if f.Required && strings.TrimSpace(secrets[f.Key]) == "" {
			errs = append(errs, fmt.Errorf("%s is required", f.Label))
		}
	}
	for key := range secrets {
		if !slices.ContainsFunc(k.Secrets, func(f Field) bool { return f.Key == key }) {
			errs = append(errs, fmt.Errorf("unknown secret %q", key))
		}
	}
	switch c.Kind {
	case KindSalesforce:
		if o := c.Setting("object"); o != "Contact" && o != "Lead" {
			errs = append(errs, errors.New("the Salesforce object is Contact or Lead"))
		}
		for _, m := range c.Mapping {
			if !SalesforceFieldRe.MatchString(m.Remote) {
				errs = append(errs, fmt.Errorf("%q isn't a Salesforce field name (letters, digits and _, like MailingCity or Score__c)", m.Remote))
			}
		}
	case KindShopify:
		if s := c.Config["shop"]; s != "" && !shopRe(s) {
			errs = append(errs, errors.New("the shop domain looks like your-store.myshopify.com"))
		}
	}
	if c.SyncMinutes != 0 && (c.SyncMinutes < 15 || c.SyncMinutes > 1440) {
		errs = append(errs, errors.New("pull every 15 to 1440 minutes, or 0 for never"))
	}
	if len(c.Mapping) > 0 && !k.Pull && !k.Push {
		errs = append(errs, errors.New("this integration has no field mapping"))
	}
	if len(c.Mapping) > 100 {
		errs = append(errs, errors.New("map up to 100 fields"))
	}
	seenRemote, seenAttr := map[string]bool{}, map[string]bool{}
	for _, m := range c.Mapping {
		if m.Dir != "" && m.Dir != "both" && m.Dir != "pull" && m.Dir != "push" {
			errs = append(errs, fmt.Errorf("%s: direction is both, pull or push", m.Remote))
			continue
		}
		if strings.TrimSpace(m.Remote) == "" || len(m.Remote) > 100 {
			errs = append(errs, errors.New("every mapped field needs the remote field's name"))
			continue
		}
		if !attr.ValidKey(m.Attr) {
			errs = append(errs, fmt.Errorf("%s: %q isn't a valid attribute name", m.Remote, m.Attr))
			continue
		}
		if m.pulls() {
			if why := MappableAttr(m.Attr); why != "" {
				errs = append(errs, fmt.Errorf("%s: %s", m.Remote, why))
			}
			if seenAttr[m.Attr] {
				errs = append(errs, fmt.Errorf("%q is filled by two remote fields", m.Attr))
			}
			seenAttr[m.Attr] = true
		}
		if m.pushes() {
			if seenRemote[m.Remote] {
				errs = append(errs, fmt.Errorf("%s is written from two attributes", m.Remote))
			}
			seenRemote[m.Remote] = true
		}
	}
	return errors.Join(errs...)
}

func shopRe(s string) bool {
	s = strings.ToLower(s)
	if !strings.HasSuffix(s, ".myshopify.com") {
		return false
	}
	name := strings.TrimSuffix(s, ".myshopify.com")
	return name != "" && strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
}

// pullAttrs turns a remote record's fields into Pulse attributes through the mapping.
func pullAttrs(mapping []FieldMap, fields map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range mapping {
		if !m.pulls() || MappableAttr(m.Attr) != "" {
			continue
		}
		v, ok := fields[m.Remote]
		if !ok || v == nil {
			continue
		}
		if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
			continue
		}
		out[m.Attr] = v
	}
	return out
}

// PushFields turns a contact into remote fields through the mapping (empty values are left out,
// so a push never blanks a field in the CRM).
func PushFields(mapping []FieldMap, email string, attrs map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range mapping {
		if !m.pushes() {
			continue
		}
		var v any
		if m.Attr == "email" && email != "" {
			v = email
		} else {
			v = attrs[m.Attr]
		}
		if v == nil || attr.String(v) == "" {
			continue
		}
		switch x := v.(type) {
		case []any:
			parts := make([]string, len(x))
			for i := range x {
				parts[i] = attr.String(x[i])
			}
			v = strings.Join(parts, ";")
		case map[string]any:
			continue
		}
		out[m.Remote] = v
	}
	return out
}

// emailRemote is the remote field mapped to email ("" when none).
func emailRemote(mapping []FieldMap) string {
	for _, m := range mapping {
		if m.Attr == "email" {
			return m.Remote
		}
	}
	return ""
}

// remoteFields lists the remote fields a pull asks for, sorted.
func remoteFields(mapping []FieldMap) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range mapping {
		if m.pulls() && !seen[m.Remote] {
			seen[m.Remote] = true
			out = append(out, m.Remote)
		}
	}
	sort.Strings(out)
	return out
}
