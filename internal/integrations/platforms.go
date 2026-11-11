package integrations

// Ad platform clients for audience sync: Facebook Custom Audiences (Graph API) and Google Ads
// Customer Match (offline user data jobs). Both take SHA-256 hashes only; Pulse never sends a
// plain email address or phone number to them.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Platform is an ad platform audiences can be synced to.
type Platform interface {
	Test(ctx context.Context) error
	// Create makes a new audience and returns its id.
	Create(ctx context.Context, name string) (string, error)
	Add(ctx context.Context, audience string, ids []Identity) error
	Remove(ctx context.Context, audience string, ids []Identity) error
}

// PlatformBatch is how many identities one upload call carries.
const PlatformBatch = 5000

// NewPlatform builds the connector's ad platform client.
func NewPlatform(c Connector, client *http.Client) (Platform, error) {
	switch c.Kind {
	case KindFacebook:
		return &facebook{base: trimSlash(c.Setting("base_url")), account: "act_" + strings.TrimPrefix(strings.TrimSpace(c.Setting("ad_account_id")), "act_"),
			call: caller{client: client, headers: map[string]string{"Authorization": "Bearer " + c.Secrets["access_token"]}}}, nil
	case KindGoogleAds:
		return &googleAds{conn: c, client: client, customer: digits(c.Setting("customer_id"))}, nil
	}
	return nil, fmt.Errorf("%s isn't an ad platform", c.Kind)
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---- Facebook ----

type facebook struct {
	base, account string
	call          caller
}

func (f *facebook) Test(ctx context.Context) error {
	_, err := f.call.do(ctx, http.MethodGet, f.base+"/"+f.account+"?fields=name,account_status", nil, nil)
	return err
}

func (f *facebook) Create(ctx context.Context, name string) (string, error) {
	var resp struct {
		ID string `json:"id"`
	}
	_, err := f.call.do(ctx, http.MethodPost, f.base+"/"+f.account+"/customaudiences", map[string]any{
		"name": name, "subtype": "CUSTOM", "description": "Synced from a Pulse segment", "customer_file_source": "USER_PROVIDED_ONLY",
	}, &resp)
	if err == nil && resp.ID == "" {
		err = errors.New("Facebook returned no audience id")
	}
	return resp.ID, err
}

func fbPayload(ids []Identity) map[string]any {
	data := make([][]string, len(ids))
	for i, id := range ids {
		data[i] = []string{id.EmailHash, id.PhoneHash}
	}
	return map[string]any{"payload": map[string]any{"schema": []string{"EMAIL_SHA256", "PHONE_SHA256"}, "data": data}}
}

var fbAudienceRe = regexp.MustCompile(`^[0-9]{1,30}$`)

func (f *facebook) users(ctx context.Context, method, audience string, ids []Identity) error {
	if !fbAudienceRe.MatchString(audience) {
		return fmt.Errorf("%q isn't a Facebook audience id", audience)
	}
	_, err := f.call.do(ctx, method, f.base+"/"+audience+"/users", fbPayload(ids), nil)
	return err
}

func (f *facebook) Add(ctx context.Context, audience string, ids []Identity) error {
	return f.users(ctx, http.MethodPost, audience, ids)
}

func (f *facebook) Remove(ctx context.Context, audience string, ids []Identity) error {
	return f.users(ctx, http.MethodDelete, audience, ids)
}

// ---- Google Ads ----

type googleAds struct {
	conn     Connector
	client   *http.Client
	customer string
}

var googleTokens = struct {
	sync.Mutex
	m map[string]gToken
}{m: map[string]gToken{}}

type gToken struct {
	access string
	exp    time.Time
}

func (g *googleAds) token(ctx context.Context) (string, error) {
	s := g.conn.Secrets
	key := g.conn.Setting("token_url") + "|" + s["client_id"] + "|" + Sha256Hex(s["refresh_token"])
	googleTokens.Lock()
	t, ok := googleTokens.m[key]
	googleTokens.Unlock()
	if ok && time.Now().Before(t.exp) {
		return t.access, nil
	}
	var resp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {s["client_id"]}, "client_secret": {s["client_secret"]}, "refresh_token": {s["refresh_token"]}}
	if _, err := (caller{client: g.client}).do(ctx, http.MethodPost, g.conn.Setting("token_url"), form, &resp); err != nil {
		return "", fmt.Errorf("sign in to Google: %w", err)
	}
	if resp.AccessToken == "" {
		return "", errors.New("sign in to Google: no access token in the answer")
	}
	life := 30 * time.Minute
	if resp.ExpiresIn > 120 && time.Duration(resp.ExpiresIn-60)*time.Second < life {
		life = time.Duration(resp.ExpiresIn-60) * time.Second
	}
	googleTokens.Lock()
	googleTokens.m[key] = gToken{resp.AccessToken, time.Now().Add(life)}
	googleTokens.Unlock()
	return resp.AccessToken, nil
}

func (g *googleAds) api(ctx context.Context) (caller, error) {
	tok, err := g.token(ctx)
	if err != nil {
		return caller{}, err
	}
	h := map[string]string{"Authorization": "Bearer " + tok, "developer-token": g.conn.Secrets["developer_token"]}
	if m := digits(g.conn.Setting("login_customer_id")); m != "" {
		h["login-customer-id"] = m
	}
	return caller{client: g.client, headers: h}, nil
}

func (g *googleAds) base() string { return trimSlash(g.conn.Setting("base_url")) }

func (g *googleAds) Test(ctx context.Context) error {
	c, err := g.api(ctx)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPost, g.base()+"/customers/"+g.customer+"/googleAds:search",
		map[string]any{"query": "SELECT customer.id FROM customer LIMIT 1"}, nil)
	return err
}

func (g *googleAds) Create(ctx context.Context, name string) (string, error) {
	c, err := g.api(ctx)
	if err != nil {
		return "", err
	}
	var resp struct {
		Results []struct {
			ResourceName string `json:"resourceName"`
		} `json:"results"`
	}
	_, err = c.do(ctx, http.MethodPost, g.base()+"/customers/"+g.customer+"/userLists:mutate", map[string]any{
		"operations": []any{map[string]any{"create": map[string]any{
			"name": name, "description": "Synced from a Pulse segment", "membershipLifeSpan": 10000,
			"crmBasedUserList": map[string]any{"uploadKeyType": "CONTACT_INFO", "dataSourceType": "FIRST_PARTY"},
		}}},
	}, &resp)
	if err != nil {
		return "", err
	}
	if len(resp.Results) == 0 || resp.Results[0].ResourceName == "" {
		return "", errors.New("Google returned no user list")
	}
	return resp.Results[0].ResourceName, nil
}

// GoogleList writes a user list id as its resource name (a plain number is a list of the account).
func GoogleList(customer, list string) string {
	list = strings.TrimSpace(list)
	if d := digits(list); d == list && d != "" {
		return "customers/" + digits(customer) + "/userLists/" + d
	}
	return list
}

var googleListRe = regexp.MustCompile(`^customers/[0-9]+/userLists/[0-9]+$`)

// upload runs one offline user data job: create, add the operations, run. Google processes the
// job in the background (matching takes hours).
func (g *googleAds) upload(ctx context.Context, audience, op string, ids []Identity) error {
	list := GoogleList(g.customer, audience)
	if !googleListRe.MatchString(list) {
		return fmt.Errorf("%q isn't a Google Ads user list", audience)
	}
	c, err := g.api(ctx)
	if err != nil {
		return err
	}
	var job struct {
		ResourceName string `json:"resourceName"`
	}
	if _, err := c.do(ctx, http.MethodPost, g.base()+"/customers/"+g.customer+"/offlineUserDataJobs:create", map[string]any{
		"job": map[string]any{"type": "CUSTOMER_MATCH_USER_LIST", "customerMatchUserListMetadata": map[string]any{"userList": list}},
	}, &job); err != nil {
		return err
	}
	if job.ResourceName == "" {
		return errors.New("Google returned no upload job")
	}
	ops := make([]any, 0, len(ids))
	for _, id := range ids {
		var uids []any
		if id.EmailHash != "" {
			uids = append(uids, map[string]any{"hashedEmail": id.EmailHash})
		}
		if id.PhoneHash != "" {
			uids = append(uids, map[string]any{"hashedPhoneNumber": id.PhoneHash})
		}
		ops = append(ops, map[string]any{op: map[string]any{"userIdentifiers": uids}})
	}
	if _, err := c.do(ctx, http.MethodPost, g.base()+"/"+job.ResourceName+":addOperations",
		map[string]any{"operations": ops, "enablePartialFailure": true}, nil); err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPost, g.base()+"/"+job.ResourceName+":run", map[string]any{}, nil)
	return err
}

func (g *googleAds) Add(ctx context.Context, audience string, ids []Identity) error {
	return g.upload(ctx, audience, "create", ids)
}

func (g *googleAds) Remove(ctx context.Context, audience string, ids []Identity) error {
	return g.upload(ctx, audience, "remove", ids)
}
