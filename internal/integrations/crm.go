package integrations

// CRM connectors: HubSpot (private app token) and Salesforce (connected app, client credentials).
// Both pull records modified since a watermark, oldest first, and push one contact: update the
// record Pulse knows (its id is kept on the contact), else the one with the contact's email, else
// create it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"pulse/internal/attr"
)

// Record is one contact pulled from a CRM.
type Record struct {
	RemoteID string
	Modified time.Time
	Fields   map[string]any
}

// RemoteField is a field of the CRM's contact object, for the mapping picker.
type RemoteField struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Type     string `json:"type,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// CRM is what a CRM connector does.
type CRM interface {
	Test(ctx context.Context) error
	Fields(ctx context.Context) ([]RemoteField, error)
	// Pull returns up to limit records modified at or after since, oldest first; more reports
	// whether there are others after them.
	Pull(ctx context.Context, since time.Time, fields []string, limit int) (recs []Record, more bool, err error)
	// Push updates or creates the contact and returns its remote id.
	Push(ctx context.Context, remoteID, email string, fields map[string]any) (string, error)
}

// NewCRM builds the connector's CRM client.
func NewCRM(c Connector, client *http.Client) (CRM, error) {
	switch c.Kind {
	case KindHubSpot:
		return &hubspot{base: trimSlash(c.Setting("base_url")), call: caller{client: client,
			headers: map[string]string{"Authorization": "Bearer " + c.Secrets["access_token"]}}}, nil
	case KindSalesforce:
		return &salesforce{conn: c, client: client}, nil
	}
	return nil, fmt.Errorf("%s isn't a CRM", c.Kind)
}

// ---- HubSpot ----

type hubspot struct {
	base string
	call caller
}

type hsObject struct {
	ID         string            `json:"id"`
	Properties map[string]string `json:"properties"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

func (h *hubspot) Test(ctx context.Context) error {
	_, err := h.call.do(ctx, http.MethodGet, h.base+"/crm/v3/objects/contacts?limit=1", nil, nil)
	return err
}

func (h *hubspot) Fields(ctx context.Context) ([]RemoteField, error) {
	var resp struct {
		Results []struct {
			Name                 string `json:"name"`
			Label                string `json:"label"`
			Type                 string `json:"type"`
			ModificationMetadata struct {
				ReadOnlyValue bool `json:"readOnlyValue"`
			} `json:"modificationMetadata"`
		} `json:"results"`
	}
	if _, err := h.call.do(ctx, http.MethodGet, h.base+"/crm/v3/properties/contacts", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]RemoteField, 0, len(resp.Results))
	for _, p := range resp.Results {
		out = append(out, RemoteField{Name: p.Name, Label: p.Label, Type: p.Type, ReadOnly: p.ModificationMetadata.ReadOnlyValue})
	}
	return out, nil
}

const hsPage = 100

func (h *hubspot) Pull(ctx context.Context, since time.Time, fields []string, limit int) ([]Record, bool, error) {
	props := append([]string{"lastmodifieddate"}, fields...)
	var out []Record
	after := ""
	for len(out) < limit {
		body := map[string]any{
			"filterGroups": []any{map[string]any{"filters": []any{map[string]any{
				"propertyName": "lastmodifieddate", "operator": "GTE", "value": strconv.FormatInt(since.UnixMilli(), 10)}}}},
			"sorts":      []any{map[string]any{"propertyName": "lastmodifieddate", "direction": "ASCENDING"}},
			"properties": props,
			"limit":      min(hsPage, limit-len(out)),
		}
		if after != "" {
			body["after"] = after
		}
		var resp struct {
			Results []hsObject `json:"results"`
			Paging  *struct {
				Next struct {
					After string `json:"after"`
				} `json:"next"`
			} `json:"paging"`
		}
		if _, err := h.call.do(ctx, http.MethodPost, h.base+"/crm/v3/objects/contacts/search", body, &resp); err != nil {
			return out, false, err
		}
		for _, o := range resp.Results {
			r := Record{RemoteID: o.ID, Modified: o.UpdatedAt, Fields: map[string]any{}}
			if t, err := time.Parse(time.RFC3339, o.Properties["lastmodifieddate"]); err == nil {
				r.Modified = t
			}
			for k, v := range o.Properties {
				if k != "lastmodifieddate" && k != "hs_object_id" && k != "createdate" {
					r.Fields[k] = v
				}
			}
			out = append(out, r)
		}
		if resp.Paging == nil || resp.Paging.Next.After == "" || len(resp.Results) == 0 {
			return out, false, nil
		}
		after = resp.Paging.Next.After
	}
	return out, true, nil
}

func hsProps(fields map[string]any) map[string]string {
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		out[k] = attr.String(v)
	}
	return out
}

func (h *hubspot) Push(ctx context.Context, remoteID, email string, fields map[string]any) (string, error) {
	body := map[string]any{"properties": hsProps(fields)}
	var got hsObject
	if remoteID != "" {
		_, err := h.call.do(ctx, http.MethodPatch, h.base+"/crm/v3/objects/contacts/"+url.PathEscape(remoteID), body, &got)
		if err == nil {
			return got.ID, nil
		}
		if StatusOf(err) != http.StatusNotFound {
			return "", err
		}
	}
	if email != "" {
		_, err := h.call.do(ctx, http.MethodPatch, h.base+"/crm/v3/objects/contacts/"+url.PathEscape(email)+"?idProperty=email", body, &got)
		if err == nil {
			return got.ID, nil
		}
		if StatusOf(err) != http.StatusNotFound {
			return "", err
		}
	}
	if _, err := h.call.do(ctx, http.MethodPost, h.base+"/crm/v3/objects/contacts", body, &got); err != nil {
		return "", err
	}
	return got.ID, nil
}

// ---- Salesforce ----

type salesforce struct {
	conn   Connector
	client *http.Client
}

type sfToken struct {
	access, instance string
	exp              time.Time
}

var sfTokens = struct {
	sync.Mutex
	m map[string]sfToken
}{m: map[string]sfToken{}}

// SalesforceFieldRe is what a Salesforce field name looks like (it goes into SOQL).
var SalesforceFieldRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,79}$`)

func (s *salesforce) object() string { return s.conn.Setting("object") }

// token gets (and caches for 30 minutes) an access token with the client credentials flow.
func (s *salesforce) token(ctx context.Context) (sfToken, error) {
	key := s.conn.Setting("login_url") + "|" + s.conn.Secrets["client_id"] + "|" + Sha256Hex(s.conn.Secrets["client_secret"])
	sfTokens.Lock()
	t, ok := sfTokens.m[key]
	sfTokens.Unlock()
	if ok && time.Now().Before(t.exp) {
		return t, nil
	}
	var resp struct {
		AccessToken string `json:"access_token"`
		InstanceURL string `json:"instance_url"`
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {s.conn.Secrets["client_id"]}, "client_secret": {s.conn.Secrets["client_secret"]}}
	if _, err := (caller{client: s.client}).do(ctx, http.MethodPost, trimSlash(s.conn.Setting("login_url"))+"/services/oauth2/token", form, &resp); err != nil {
		return sfToken{}, fmt.Errorf("sign in to Salesforce: %w", err)
	}
	if resp.AccessToken == "" {
		return sfToken{}, errors.New("sign in to Salesforce: no access token in the answer")
	}
	t = sfToken{access: resp.AccessToken, instance: trimSlash(resp.InstanceURL), exp: time.Now().Add(30 * time.Minute)}
	if t.instance == "" {
		t.instance = trimSlash(s.conn.Setting("login_url"))
	}
	sfTokens.Lock()
	sfTokens.m[key] = t
	sfTokens.Unlock()
	return t, nil
}

// api returns a caller and the REST base (…/services/data/vNN.0).
func (s *salesforce) api(ctx context.Context) (caller, string, error) {
	t, err := s.token(ctx)
	if err != nil {
		return caller{}, "", err
	}
	return caller{client: s.client, headers: map[string]string{"Authorization": "Bearer " + t.access}},
		t.instance + "/services/data/" + s.conn.Setting("api_version"), nil
}

func (s *salesforce) query(ctx context.Context, soql string, out any) error {
	c, base, err := s.api(ctx)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodGet, base+"/query?q="+url.QueryEscape(soql), nil, out)
	return err
}

func (s *salesforce) Test(ctx context.Context) error {
	return s.query(ctx, "SELECT Id FROM "+s.object()+" LIMIT 1", nil)
}

func (s *salesforce) Fields(ctx context.Context) ([]RemoteField, error) {
	c, base, err := s.api(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Fields []struct {
			Name       string `json:"name"`
			Label      string `json:"label"`
			Type       string `json:"type"`
			Updateable bool   `json:"updateable"`
		} `json:"fields"`
	}
	if _, err := c.do(ctx, http.MethodGet, base+"/sobjects/"+s.object()+"/describe", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]RemoteField, 0, len(resp.Fields))
	for _, f := range resp.Fields {
		out = append(out, RemoteField{Name: f.Name, Label: f.Label, Type: f.Type, ReadOnly: !f.Updateable})
	}
	return out, nil
}

const sfTime = "2006-01-02T15:04:05.000-0700"

func (s *salesforce) Pull(ctx context.Context, since time.Time, fields []string, limit int) ([]Record, bool, error) {
	cols := []string{"Id", "LastModifiedDate"}
	for _, f := range fields {
		if SalesforceFieldRe.MatchString(f) && f != "Id" && f != "LastModifiedDate" {
			cols = append(cols, f)
		}
	}
	soql := fmt.Sprintf("SELECT %s FROM %s WHERE LastModifiedDate >= %s ORDER BY LastModifiedDate ASC LIMIT %d",
		strings.Join(cols, ", "), s.object(), since.UTC().Format("2006-01-02T15:04:05Z"), limit+1)
	c, base, err := s.api(ctx)
	if err != nil {
		return nil, false, err
	}
	next := base + "/query?q=" + url.QueryEscape(soql)
	var out []Record
	for next != "" {
		var resp struct {
			Records        []map[string]any `json:"records"`
			NextRecordsURL string           `json:"nextRecordsUrl"`
		}
		if _, err := c.do(ctx, http.MethodGet, next, nil, &resp); err != nil {
			return out, false, err
		}
		for _, rec := range resp.Records {
			r := Record{RemoteID: attr.String(rec["Id"]), Fields: map[string]any{}}
			r.Modified, _ = time.Parse(sfTime, attr.String(rec["LastModifiedDate"]))
			for k, v := range rec {
				if k != "attributes" && k != "Id" && k != "LastModifiedDate" {
					r.Fields[k] = v
				}
			}
			out = append(out, r)
		}
		next = ""
		if resp.NextRecordsURL != "" && len(out) <= limit {
			t, _ := s.token(ctx)
			next = t.instance + resp.NextRecordsURL
		}
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// soqlString quotes s for a SOQL string literal.
func soqlString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func (s *salesforce) Push(ctx context.Context, remoteID, email string, fields map[string]any) (string, error) {
	c, base, err := s.api(ctx)
	if err != nil {
		return "", err
	}
	obj := base + "/sobjects/" + s.object()
	if remoteID != "" {
		_, err := c.do(ctx, http.MethodPatch, obj+"/"+url.PathEscape(remoteID), fields, nil)
		if err == nil {
			return remoteID, nil
		}
		if StatusOf(err) != http.StatusNotFound {
			return "", err
		}
	}
	if email != "" {
		var found struct {
			Records []struct {
				ID string `json:"Id"`
			} `json:"records"`
		}
		if err := s.query(ctx, "SELECT Id FROM "+s.object()+" WHERE Email = "+soqlString(email)+" LIMIT 1", &found); err != nil {
			return "", err
		}
		if len(found.Records) > 0 {
			id := found.Records[0].ID
			_, err := c.do(ctx, http.MethodPatch, obj+"/"+url.PathEscape(id), fields, nil)
			return id, err
		}
	}
	// Salesforce requires a last name (and, on leads, a company) to create a record.
	create := make(map[string]any, len(fields)+2)
	for k, v := range fields {
		create[k] = v
	}
	if attr.String(create["LastName"]) == "" {
		create["LastName"] = "[not provided]"
	}
	if s.object() == "Lead" && attr.String(create["Company"]) == "" {
		create["Company"] = "[not provided]"
	}
	var made struct {
		ID string `json:"id"`
	}
	if _, err := c.do(ctx, http.MethodPost, obj, create, &made); err != nil {
		return "", err
	}
	return made.ID, nil
}
