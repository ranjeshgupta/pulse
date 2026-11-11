package personalize

import (
	"strings"
	"testing"
	"time"

	"pulse/internal/model"
)

func vars() Vars {
	return Vars{
		Contact: model.Contact{ExternalID: "u1", Email: "asha@example.com", Attrs: map[string]any{
			"first_name": "asha rao", "city": "Pune", "phone": "+919800000001", "phone_country": "IN",
			"products": []any{"KIT-1", "BED-2"}, "address": map[string]any{"pin": "411001"}, "timezone": "Asia/Kolkata",
		}},
		Event: map[string]any{
			"cart_id": "c-9", "total": 123456.5, "currency": "INR", "at": "2026-11-10T20:30:00Z", "day": "2026-11-12",
			"items": []any{
				map[string]any{"name": "Yoga mat", "price": 999.0},
				map[string]any{"name": "Water bottle", "price": 349.0},
			},
			"note": "a & b <c>",
		},
		Campaign: Campaign{ID: 7, Name: "Cart rescue", Type: "journey", Step: "s1", Channel: "email", Template: "Cart reminder"},
		Links:    map[string]string{"unsubscribe": "https://p.example.com/u/tok", "preferences": "https://p.example.com/p/tok"},
	}
}

type legacy struct {
	Contact model.Contact
	Attrs   map[string]any
}

func text(t *testing.T, v Vars, src string) (string, []string) {
	t.Helper()
	r := New(v)
	out, err := r.Text("f", src, legacy{v.Contact, v.Contact.Attrs})
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return out, r.Missing()
}

func TestVariablesAndPipes(t *testing.T) {
	cases := map[string]string{
		"{{contact.first_name}}":                         "asha rao",
		"{{ contact.first_name | title }}":               "Asha Rao",
		"{{contact.first_name | upper}}":                 "ASHA RAO",
		"{{contact.city | lower}}":                       "pune",
		"{{contact.email}}":                              "asha@example.com",
		"{{contact.external_id}}":                        "u1",
		"{{contact.phone_country}}":                      "IN",
		"{{contact.products}}":                           "KIT-1, BED-2",
		"{{contact.address.pin}}":                        "411001",
		"{{contact.nickname | default:\"there\"}}":       "there",
		"{{contact.nickname | default:'friend' | upper}}": "FRIEND",
		"{{event.items.0.name}}":                         "Yoga mat",
		"{{event.items.1.price | money:\"INR\"}}":        "₹349.00",
		"{{event.items[].name}}":                         "Yoga mat, Water bottle",
		"{{event.total | money}}":                        "₹1,23,456.50",
		"{{event.total | money:\"USD\"}}":                "$123,456.50",
		"{{event.total | money:\"XYZ\"}}":                "XYZ 123,456.50",
		"{{event.total | number}}":                       "123,456.50",
		"{{event.total | number:0}}":                     "123,457",
		"{{event.cart_id}}":                              "c-9",
		"{{event.at | date:\"2 Jan 2006 15:04\"}}":       "11 Nov 2026 02:00", // in the contact's zone (IST)
		"{{event.day | date}}":                           "12 Nov 2026",       // a calendar date stays that day
		"{{event.items.0.name | truncate:4}}":            "Yog…",
		"{{event.items.0.name | truncate:40}}":           "Yoga mat",
		"{{event.items.0.name | urlencode}}":            "Yoga+mat",
		"{{event.items.0.name | json}}":                  `"Yoga mat"`,
		"{{campaign.name}} #{{campaign.id}} {{campaign.step}} {{campaign.type}} {{campaign.channel}} {{campaign.template}}": "Cart rescue #7 s1 journey email Cart reminder",
		"{{links.unsubscribe}}":                         "https://p.example.com/u/tok",
		"{{links.preferences}}":                         "https://p.example.com/p/tok",
		"Hi {{.Attrs.first_name}} from {{.Attrs.city}}": "Hi asha rao from Pune",          // the older syntax
		"{{.Contact.Email}} / {{contact.city}}":          "asha@example.com / Pune",        // mixed
		"{{if .Attrs.city}}in {{contact.city}}{{end}}":   "in Pune",                        // Go actions around
		"{{- contact.city -}} !":                         "Pune!",                          // trim markers
		"{{event.note}}":                                 "a & b <c>",                      // text is not escaped
		"{{event.missing | default:\"0\" | money}}":      "₹0.00",
	}
	for src, want := range cases {
		if got, _ := text(t, vars(), src); got != want {
			t.Errorf("%s = %q, want %q", src, got, want)
		}
	}
}

func TestHTMLIsEscaped(t *testing.T) {
	v := vars()
	v.Contact.Attrs["first_name"] = `<script>alert(1)</script>`
	r := New(v)
	out, err := r.HTML("html", `<p>Hi {{contact.first_name}}, {{event.note}}</p><a href="{{links.unsubscribe}}">x</a>`+
		`<a href="https://shop.example.com/search?q={{event.items.0.name | urlencode}}">s</a>`+
		`<a title="{{contact.nickname | default:&quot;you &amp; me&quot;}}">t</a>`, legacy{v.Contact, v.Contact.Attrs})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"&lt;script&gt;", "a &amp; b &lt;c&gt;", `href="https://p.example.com/u/tok"`, "q=Yoga&#43;mat", `title="you &amp; me"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if strings.Contains(out, "<script>") {
		t.Errorf("not escaped: %s", out)
	}
	// A value can't become a javascript: link.
	v.Event = map[string]any{"u": "javascript:alert(1)"}
	out, _ = New(v).HTML("html", `<a href="{{event.u}}">x</a>`, nil)
	if strings.Contains(out, "javascript:") {
		t.Errorf("unsafe link kept: %s", out)
	}
}

func TestMissingValues(t *testing.T) {
	v := vars()
	v.Event = nil // a broadcast
	out, missing := text(t, v, "Hi {{contact.nickname}}, {{event.cart_id}} {{contact.city}} {{contact.zip | default:\"-\"}} {{.Attrs.nope}}")
	if out != "Hi ,  Pune - " {
		t.Errorf("out = %q", out)
	}
	if strings.Join(missing, ",") != "contact.nickname,event.cart_id" {
		t.Errorf("missing = %v (defaults and older tags don't count)", missing)
	}
	v.Contact.Attrs["blank"] = "  "
	v.Contact.Attrs["none"] = []any{}
	if _, m := text(t, v, "{{contact.blank}}{{contact.none}}"); len(m) != 2 {
		t.Errorf("blank text and empty lists are missing: %v", m)
	}
	v.Contact.Attrs["zero"] = 0.0
	if out, m := text(t, v, "{{contact.zero}}"); out != "0" || len(m) != 0 {
		t.Errorf("zero is a value: %q %v", out, m)
	}
}

func TestTranslateErrors(t *testing.T) {
	bad := map[string]string{
		"{{contact.first_name | shout}}":     "unknown pipe",
		"{{contact.first_name | default}}":   "default needs a value",
		"{{contact.first_name | upper:1}}":   "upper takes no value",
		"{{contact.x | truncate:abc}}":       "truncate needs a number",
		"{{contact.x | number:9}}":           "number takes 0 to 6",
		"{{contact.}}":                       "name a contact field",
		"{{contact.a-b}}":                    "isn't a field name",
		"{{campaign.owner}}":                 "campaign has",
		"{{links.webview}}":                  "links has",
		"{{event.a[].b[].c}}":                "one list",
		"{{contact.x | default:\"open}}":     "",
		"{{contact.x | default:two words}}":  "put the value in quotes",
	}
	for src, want := range bad {
		_, _, err := Translate(src, false)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", src, err, want)
		}
		if Check("f", src, false) == nil {
			t.Errorf("%s: Check passed", src)
		}
	}
	if err := Check("f", "{{.Attrs.first_name}} {{contact.first_name | default:\"x\" | title}}", false); err != nil {
		t.Errorf("valid template: %v", err)
	}
	if Check("f", "{{nope}}", false) == nil {
		t.Error("unknown Go function passed")
	}
}

func TestRefs(t *testing.T) {
	refs := Refs(`Hi {{contact.first_name | default:"there"}}, {{event.items[].name}} {{.Attrs.city}} {{links.unsubscribe}}`)
	if len(refs) != 3 || refs[0].Var != "contact.first_name" || !refs[0].HasDefault || refs[1].Var != "event.items[].name" {
		t.Errorf("refs = %+v", refs)
	}
	got := strings.Join(PipeNames(), ",")
	if got != "date,default,json,lower,money,number,title,truncate,upper,urlencode" {
		t.Errorf("pipes = %s (keep web/src/personalize.ts in step)", got)
	}
}

func TestFormatHelpers(t *testing.T) {
	if Format(3000000.0) != "3000000" || Format(1.5) != "1.5" || Format(true) != "true" {
		t.Error("numbers and booleans")
	}
	if Format(map[string]any{"a": 1.0}) != `{"a":1}` {
		t.Error("objects print as JSON")
	}
	if formatMoney(-1234.5, "INR") != "-₹1,234.50" || formatMoney(10000000.0, "INR") != "₹1,00,00,000.00" {
		t.Errorf("INR: %s %s", formatMoney(-1234.5, "INR"), formatMoney(10000000.0, "INR"))
	}
	if formatMoney(1234.0, "JPY") != "¥1,234" || formatMoney("12.3", "gbp") != "£12.30" {
		t.Error("JPY and GBP")
	}
	if formatMoney("n/a", "INR") != "n/a" || formatNumber("n/a", -1) != "n/a" {
		t.Error("non-numbers pass through")
	}
	if formatDate(1762770600.0, "2006-01-02", nil0()) != "2025-11-10" || formatDate("soon", "2006", nil0()) != "soon" {
		t.Error("unix seconds and non-dates")
	}
	if titleCase("ASHA rao-kumar") != "Asha Rao-Kumar" {
		t.Error(titleCase("ASHA rao-kumar"))
	}
}

func nil0() *time.Location { return time.UTC }
