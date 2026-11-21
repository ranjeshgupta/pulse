package spamcheck

import (
	"strings"
	"testing"
)

func has(r Result, rule string) bool {
	for _, i := range r.Issues {
		if i.Rule == rule {
			return true
		}
	}
	return false
}

func TestClean(t *testing.T) {
	r := Check(Email{Subject: "Your November order summary", HTML: "<p>Hi Asha,</p><p>" + strings.Repeat("Here is what you ordered this month and when it arrives. ", 6) +
		`</p><p><a href="https://acme.test/orders">See your orders</a></p>`, Text: "Hi Asha"})
	if r.Rating != "good" || r.Score != 0 || r.Links != 1 || !strings.HasPrefix(r.Preheader, "Hi Asha") {
		t.Errorf("clean email: %+v", r)
	}
}

func TestSpammy(t *testing.T) {
	r := Check(Email{Subject: "FREE MONEY!!! ACT NOW", HTML: `<img src="x.png"><a href="https://bit.ly/x">Claim</a>
		<a href="https://evil.test/x">www.mybank.com</a><div style="display:none">hidden</div> guaranteed winner $$$`})
	for _, rule := range []string{"subject_caps", "subject_bangs", "spam_phrases", "image_heavy", "img_alt", "shortener", "link_mismatch", "hidden_text", "money", "no_text_part"} {
		if !has(r, rule) {
			t.Errorf("missing %s: %+v", rule, r.Issues)
		}
	}
	if r.Rating != "spam" {
		t.Errorf("rating %s (%v)", r.Rating, r.Score)
	}
}

func TestClipAndMissingSubject(t *testing.T) {
	r := Check(Email{HTML: "<p>" + strings.Repeat("word ", 30000) + "</p>", Text: "x"})
	if !has(r, "gmail_clip") || !has(r, "subject_missing") {
		t.Errorf("%+v", r.Issues)
	}
	if has(Check(Email{Subject: "Hi", HTML: `<a href="{{unsubscribe}}">example.com</a>` + strings.Repeat(" text", 60), Text: "x"}), "link_mismatch") {
		t.Error("a templated link counted as a mismatch")
	}
}
