package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayFailureRuleAndMatchBy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "to=%2B910000000000") {
			_, _ = w.Write([]byte(`{"status":"error","reason":"invalid number"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","msgid":"abc"}`))
	}))
	defer srv.Close()
	g := customGateway(t, CustomRequest{Method: "GET", URL: srv.URL + "/send?to={{message.to}}&ref={{message.id}}", IDPath: "msgid",
		Failure: &FailureRule{JSONPath: "status", Values: []string{"error"}, Unreachable: true}}, nil, nil)
	if _, err := g.Send(context.Background(), Outbound{MessageID: "m1", Channel: "sms", To: "+910000000000"}); !errors.Is(err, ErrUnreachable) {
		t.Errorf("failure rule: %v, want unreachable (fallback)", err)
	}
	res, err := g.Send(context.Background(), Outbound{MessageID: "m2", Channel: "sms", To: "+919812345678"})
	if err != nil || res.ProviderMessageID != "abc" {
		t.Errorf("send: %+v %v", res, err)
	}
	g.Custom.MatchBy = MatchPulse
	if res, _ := g.Send(context.Background(), Outbound{MessageID: "m3", Channel: "sms", To: "+919812345678"}); res.ProviderMessageID != "m3" {
		t.Errorf("match by Pulse id: %+v", res)
	}
	// Dry runs read pasted answers the same way.
	if _, err := g.CheckAnswer(200, []byte(`{"status":"ERROR"}`)); !errors.Is(err, ErrUnreachable) {
		t.Errorf("check answer: %v", err)
	}
}

func TestGatewayPoll(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			asked = r.URL.Query().Get("id")
			_, _ = w.Write([]byte(`{"data":{"state":"DELIVRD"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"p-9"}`))
	}))
	defer srv.Close()
	cr := CustomRequest{Method: "POST", URL: srv.URL + "/send", Body: `{"to":"{{message.to}}"}`, IDPath: "id",
		Receipt: &ReceiptMapping{IDPath: "data.id", StatusPath: "data.state", Statuses: map[string]string{"DELIVRD": "delivered", "SUBMITTED": "pending"}},
		Poll:    &PollRequest{Method: "GET", URL: srv.URL + "/status?id={{message.provider_id}}", EveryMinutes: 5}}
	if err := cr.Validate(); err != nil {
		t.Fatal(err)
	}
	g := customGateway(t, cr, nil, nil)
	every, follow, on := g.Polls()
	if !on || every.Minutes() != 5 || follow.Hours() != 24 {
		t.Errorf("polls: %v %v %v", every, follow, on)
	}
	rs, err := g.Poll(context.Background(), Outbound{MessageID: "m1", ProviderMessageID: "p-9"}, "p-9")
	if err != nil || asked != "p-9" || len(rs) != 1 || rs[0].Status != "delivered" || rs[0].ProviderMessageID != "p-9" {
		t.Errorf("poll: asked %q, %+v %v", asked, rs, err)
	}
	// Reports pasted in setup: a list, with pending statuses read too.
	rs, err = g.CheckReport([]byte(`[{"data":{"id":"a","state":"SUBMITTED"}},{"data":{"id":"b","state":"DELIVRD"}}]`))
	if err != nil || len(rs) != 2 || rs[0].Status != "pending" || rs[1].ProviderMessageID != "b" {
		t.Errorf("check report: %+v %v", rs, err)
	}
	// Polled only: the webhook refuses everything.
	if err := (CustomRequest{Method: "GET", URL: "https://x.test/s", Receipt: &ReceiptMapping{IDPath: "id", StatusPath: "s", Statuses: map[string]string{"a": "delivered"}}}).Validate(); err == nil {
		t.Error("a webhook mapping without verification passed")
	}
}

func TestPollReceiptsPicksTheMessage(t *testing.T) {
	o := Outbound{MessageID: "m1", ProviderMessageID: "p1"}
	rs := pollReceipts([]Receipt{{ProviderMessageID: "p0", Status: "delivered"}, {ProviderMessageID: "p1", Status: "failed"}}, o, "p1")
	if len(rs) != 1 || rs[0].Status != "failed" {
		t.Errorf("by id: %+v", rs)
	}
	if rs := pollReceipts([]Receipt{{ProviderMessageID: "other", Status: "delivered"}}, o, "p1"); len(rs) != 1 || rs[0].ProviderMessageID != "p1" {
		t.Errorf("single answer: %+v", rs)
	}
}
