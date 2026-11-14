package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"pulse/internal/channel"
)

func TestCatalogKinds(t *testing.T) {
	for _, id := range []string{"twilio", "msg91", "meta_whatsapp", "google_rbm", "fcm"} {
		if _, ok := Lookup(id); !ok {
			t.Errorf("kind %q not registered", id)
		}
	}
	for _, k := range Catalog() {
		if k.ID == "" || k.Name == "" || k.Description == "" || k.New == nil || !slices.Contains(channel.All, k.Channel) {
			t.Errorf("incomplete kind %+v", k)
		}
		for _, f := range append(append([]Field{}, k.Config...), k.Secrets...) {
			if f.Key == "" || f.Label == "" {
				t.Errorf("%s: field without key or label: %+v", k.ID, f)
			}
		}
	}
}

// Every adapter builds from just its required settings through providers.New.
func TestNewWithRequiredSettings(t *testing.T) {
	sa := testServiceAccount(t, "https://oauth2.googleapis.com/token")
	extra := map[string]map[string]string{
		"twilio":          {"from": "+14155550123"}, // one of from / messaging_service_sid is needed
		"smtp":            {"port": "587"},
		"gmail_smtp":      {"username": "me@gmail.com", "password": "app-pass"},
		"gworkspace_smtp": {"username": "me@acme.com", "password": "app-pass"},
	}
	for _, ch := range channel.External { // standard mode: an endpoint and a signing secret
		extra["http_"+ch] = map[string]string{"url": "https://gateway.example.com/pulse", "signing_secret": "s"}
	}
	for _, k := range Catalog() {
		t.Run(k.ID, func(t *testing.T) {
			config, secrets := map[string]string{}, map[string]string{}
			for _, f := range k.Config {
				if f.Required {
					config[f.Key] = "x"
					if f.Key == "url" {
						config[f.Key] = "https://gateway.example.com/pulse"
					}
				}
			}
			for _, f := range k.Secrets {
				if f.Required {
					secrets[f.Key] = "x"
					if strings.HasSuffix(f.Key, "_json") {
						secrets[f.Key] = sa
					}
				}
			}
			for key, v := range extra[k.ID] {
				if key == "password" || key == "signing_secret" {
					secrets[key] = v
				} else {
					config[key] = v
				}
			}
			if err := k.Validate(config, secrets, true); err != nil {
				t.Fatal(err)
			}
			p, err := New(k.ID, Deps{}, config, secrets)
			if err != nil || p == nil {
				t.Fatalf("New: %v", err)
			}
		})
	}
	if _, err := New("twilio", Deps{}, map[string]string{"account_sid": "AC1"}, map[string]string{"auth_token": "t"}); err == nil {
		t.Error("twilio without from or messaging service built")
	}
	if _, err := New("fcm", Deps{}, map[string]string{"project_id": "p"}, map[string]string{"service_account_json": `{"type":"x"}`}); err == nil {
		t.Error("fcm with an invalid service account built")
	}
}

// The service account flow signs a JWT, trades it for an access token once, and reuses it.
func TestServiceAccountTokens(t *testing.T) {
	var grants atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f, _ := url.ParseQuery(string(b))
		if f.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || strings.Count(f.Get("assertion"), ".") != 2 {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		grants.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"ya29.sa","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenSrv.Close()
	api, got := fakeAPI(t, 200, `{"name":"projects/p/messages/1"}`)

	p, err := New("fcm", Deps{HTTP: api.Client()}, map[string]string{"project_id": "p"},
		map[string]string{"service_account_json": testServiceAccount(t, tokenSrv.URL)})
	if err != nil {
		t.Fatal(err)
	}
	f := p.(*FCM)
	f.baseURL = api.URL
	for range 2 {
		if _, err := f.Send(context.Background(), pushOutbound("android")); err != nil {
			t.Fatal(err)
		}
	}
	if got.Header.Get("Authorization") != "Bearer ya29.sa" || grants.Load() != 1 {
		t.Fatalf("auth %q after %d grants", got.Header.Get("Authorization"), grants.Load())
	}

	// A token endpoint rejecting the key is permanent.
	reject := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`)
	}))
	defer reject.Close()
	p, err = New("google_rbm", Deps{}, map[string]string{"agent_id": "a"},
		map[string]string{"service_account_json": testServiceAccount(t, reject.URL)})
	if err != nil {
		t.Fatal(err)
	}
	g := p.(*GoogleRBM)
	g.baseURL = api.URL
	_, err = g.Send(context.Background(), Outbound{MessageID: "m", To: "+919812345678"})
	if c := errClass(err); c != "permanent" {
		t.Fatalf("rejected key: %s (%v)", c, err)
	}
}

func TestCatalogListsAreNeverNull(t *testing.T) {
	for _, k := range Catalog() {
		if k.Config == nil || k.Secrets == nil {
			t.Errorf("%s: config or secrets is nil (sent as null)", k.ID)
		}
	}
}
