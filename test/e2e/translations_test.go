//go:build e2e

package e2e

import (
	"fmt"
	"net/url"
	"testing"
	"time"
)

// TestTemplateTranslations (O6): a template with a Hindi translation sends Hindi to a contact whose
// locale is hi-IN and the default to everyone else; a bad locale is refused.
func TestTemplateTranslations(t *testing.T) {
	c, _ := workspace(t, "Languages")
	run := fmt.Sprint(time.Now().UnixNano())
	body := map[string]any{"name": "welcome", "channel": "email", "subject": "Welcome " + run, "html": "<p>Hello {{contact.first_name}}</p>",
		"translations": []any{map[string]any{"locale": "hi", "subject": "स्वागत " + run, "html": "<p>नमस्ते {{contact.first_name}}</p>"}}}
	tpl := jsonCall(t, c, "POST", "/v1/templates", body, 201)["id"]
	if got := jsonCall(t, c, "GET", fmt.Sprintf("/v1/templates/%v", tpl), nil, 200); len(asList(got["translations"])) != 1 {
		t.Fatalf("translations not kept: %v", got)
	}
	body["translations"] = []any{map[string]any{"locale": "Hindi", "subject": "x"}}
	jsonCall(t, c, "PUT", fmt.Sprintf("/v1/templates/%v", tpl), body, 400)

	for _, p := range []struct{ ext, locale, want string }{{"hi-" + run, "hi-IN", "स्वागत " + run}, {"en-" + run, "en-GB", "Welcome " + run}} {
		jsonCall(t, c, "POST", "/v1/events", map[string]any{"external_id": p.ext, "name": "identify", "email": p.ext + "@example.com",
			"attrs": map[string]any{"first_name": "Asha", "locale": p.locale}}, 202)
		waitFor(t, 10*time.Second, func() bool { return jsonCall(t, c, "GET", "/v1/contacts/"+p.ext, nil, -1)["contact"] != nil })
		jsonCall(t, c, "POST", "/v1/channels/test", map[string]any{"template_id": tpl, "external_id": p.ext}, 202)
		var search struct {
			Messages []struct{ Subject string } `json:"messages"`
		}
		waitFor(t, 20*time.Second, func() bool {
			mailpitGet(t, "/api/v1/search?query="+url.QueryEscape("to:"+p.ext+"@example.com"), &search)
			return len(search.Messages) == 1
		})
		if search.Messages[0].Subject != p.want {
			t.Errorf("%s got %q, want %q", p.locale, search.Messages[0].Subject, p.want)
		}
	}
}
