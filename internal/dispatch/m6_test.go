package dispatch

import (
	"context"
	"encoding/json"
	"html"
	"regexp"
	"strings"
	"testing"

	"pulse/internal/amp"
	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/model"
	"pulse/internal/tracking"
)

// An email's AMP part goes out personalised, with each form posting to a signed per-recipient
// link naming the message, the contact and the block, tracked links and an open pixel.
func TestAMPPartSignedLinks(t *testing.T) {
	f := setup(t)
	box, _ := auth.NewBox(strings.Repeat("cd", 32))
	codec := tracking.New(box, strings.Repeat("cd", 32), "https://pulse.test")
	f.d.cfg.Tracking = codec
	doc := &amp.Doc{Blocks: []amp.Block{
		{ID: "h", Type: amp.TypeHeading, Text: "Hi {{contact.first_name}}"},
		{ID: "b", Type: amp.TypeButton, Text: "Shop", URL: "https://shop.example/sale"},
		{ID: "poll-1", Type: amp.TypePoll, Text: "Q?", Options: []string{"A", "B"}},
	}}
	blocks, _ := json.Marshal(doc)
	content, _ := json.Marshal(map[string]any{"amp_blocks": json.RawMessage(blocks)})
	tpl := model.Template{ID: 9, Channel: channel.Email, Subject: "s", HTML: "<p>x</p>", Content: content}
	if err := channel.PrepareAMP(&tpl); err != nil {
		t.Fatal(err)
	}
	f.st.templates[9] = tpl
	m := model.Message{ID: "bc5:1:1", TenantID: 1, ContactID: 1, Channel: channel.Email, TemplateID: 9, BroadcastID: 5, Run: 1}
	if err := f.d.Handle(context.Background(), msgRecord(m)); err != nil {
		t.Fatal(err)
	}
	if len(f.mail.sent) != 1 {
		t.Fatal("not sent")
	}
	a := f.mail.sent[0].Rendered.Content.AMP
	if !strings.Contains(a, "Hi Asha") || strings.Contains(a, "pulse:submit") || !strings.Contains(a, "/t/c/") || !strings.Contains(a, `<amp-img src="https://pulse.test/t/o/`) {
		t.Fatalf("AMP part:\n%s", a)
	}
	link := regexp.MustCompile(`action-xhr="https://pulse\.test/v1/amp/([^"]+)"`).FindStringSubmatch(a)
	if link == nil {
		t.Fatal("no post-back link")
	}
	tok, err := codec.DecodeAMP(html.UnescapeString(link[1]))
	if err != nil || tok.TenantID != 1 || tok.ContactID != 1 || tok.Block != "poll-1" || tok.MessageID != "bc5:1:1" || tok.TemplateID != 9 ||
		tok.BroadcastID != 5 || tok.ExternalID != "u1" || tok.Test || tok.Exp <= 0 {
		t.Fatalf("token %+v %v", tok, err)
	}
	if _, err := codec.Decode(html.UnescapeString(link[1])); err == nil {
		t.Error("an AMP token opened as a tracking token")
	}

	// Without a server key, forms can't post back: the AMP part is left out.
	f.d.cfg.Tracking = nil
	f.mail.sent = nil
	m.ID = "bc5:1:2"
	_ = f.d.Handle(context.Background(), msgRecord(m))
	if len(f.mail.sent) != 1 || f.mail.sent[0].Rendered.Content.AMP != "" {
		t.Error("AMP part with unfilled post-back links sent")
	}
}
