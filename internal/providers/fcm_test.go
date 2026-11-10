package providers

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"golang.org/x/oauth2"

	"pulse/internal/channel"
)

func testFCM(srv string, c *http.Client) *FCM {
	return &FCM{HTTP: c, ProjectID: "acme-app", baseURL: srv,
		tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.fcm"})}
}

func pushOutbound(platform string) Outbound {
	return Outbound{MessageID: "m1", Channel: channel.Push, To: "dTok3n:APA91b", Platform: platform,
		Rendered: channel.Rendered{Content: channel.Content{Title: "Sale", Body: "50% off today",
			ImageURL: "https://cdn.acme.com/sale.jpg", URL: "https://acme.com/sale", Data: map[string]string{"campaign": "c9"}}}}
}

func TestFCMRequest(t *testing.T) {
	srv, got := fakeAPI(t, 200, `{"name":"projects/acme-app/messages/0:1500415314455276%31bd1c9631bd1c96"}`)
	o := pushOutbound("android")
	res, err := testFCM(srv.URL, srv.Client()).Send(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.ProviderMessageID != "projects/acme-app/messages/0:1500415314455276%31bd1c9631bd1c96" {
		t.Fatalf("id = %q", res.ProviderMessageID)
	}
	if got.Method != http.MethodPost || got.Path != "/v1/projects/acme-app/messages:send" ||
		got.Header.Get("Authorization") != "Bearer ya29.fcm" {
		t.Fatalf("request %s %s %q", got.Method, got.Path, got.Header.Get("Authorization"))
	}
	m := jsonPath(decodeJSON(t, got.Body), "message")
	if jsonPath(m, "token") != "dTok3n:APA91b" || jsonPath(m, "notification", "title") != "Sale" ||
		jsonPath(m, "notification", "body") != "50% off today" ||
		jsonPath(m, "notification", "image") != "https://cdn.acme.com/sale.jpg" ||
		jsonPath(m, "data", "campaign") != "c9" || jsonPath(m, "data", "url") != "https://acme.com/sale" ||
		jsonPath(m, "webpush") != nil {
		t.Fatalf("body %s", got.Body)
	}
	if _, ok := o.Rendered.Content.Data["url"]; ok {
		t.Fatal("Send modified the template's data map")
	}

	if _, err := testFCM(srv.URL, srv.Client()).Send(context.Background(), pushOutbound("web")); err != nil {
		t.Fatal(err)
	}
	if jsonPath(decodeJSON(t, got.Body), "message", "webpush", "fcm_options", "link") != "https://acme.com/sale" {
		t.Fatalf("web body %s", got.Body)
	}

	web := pushOutbound("web")
	web.Rendered.Content.URL = "http://acme.com/sale" // not https: no link
	if _, err := testFCM(srv.URL, srv.Client()).Send(context.Background(), web); err != nil {
		t.Fatal(err)
	}
	if jsonPath(decodeJSON(t, got.Body), "message", "webpush") != nil {
		t.Fatalf("http link sent: %s", got.Body)
	}
}

func TestFCMErrors(t *testing.T) {
	fcmErr := func(code int, status, code2, msg string) string {
		return `{"error":{"code":` + strconv.Itoa(code) + `,"message":"` + msg + `","status":"` + status +
			`","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"` + code2 + `"}]}}`
	}
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"unregistered", 404, fcmErr(404, "NOT_FOUND", "UNREGISTERED", "Requested entity was not found."), "unreachable"},
		{"404 no body", 404, ``, "unreachable"},
		{"invalid token", 400, fcmErr(400, "INVALID_ARGUMENT", "INVALID_ARGUMENT",
			"The registration token is not a valid FCM registration token"), "unreachable"},
		{"sender id mismatch", 403, fcmErr(403, "PERMISSION_DENIED", "SENDER_ID_MISMATCH", "SenderId mismatch"), "unreachable"},
		{"invalid payload", 400, fcmErr(400, "INVALID_ARGUMENT", "INVALID_ARGUMENT",
			"Invalid value at 'message.data[0].value'"), "permanent"},
		{"unauthenticated", 401, fcmErr(401, "UNAUTHENTICATED", "THIRD_PARTY_AUTH_ERROR", "auth"), "permanent"},
		{"forbidden", 403, fcmErr(403, "PERMISSION_DENIED", "", "denied"), "permanent"},
		{"quota", 429, fcmErr(429, "RESOURCE_EXHAUSTED", "QUOTA_EXCEEDED", "quota"), "transient"},
		{"internal", 500, fcmErr(500, "INTERNAL", "INTERNAL", "x"), "transient"},
		{"unavailable", 503, fcmErr(503, "UNAVAILABLE", "UNAVAILABLE", "x"), "transient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := fakeAPI(t, tc.status, tc.body)
			_, err := testFCM(srv.URL, srv.Client()).Send(context.Background(), pushOutbound("ios"))
			if c := errClass(err); c != tc.want {
				t.Fatalf("got %s (%v), want %s", c, err, tc.want)
			}
		})
	}
}
