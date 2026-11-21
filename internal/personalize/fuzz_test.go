package personalize

import "testing"

var fuzzSeeds = []string{
	`Hi {{contact.first_name | default:"there" | title}}`,
	`{{event.total | money:"INR"}} {{event.at | date:"2 Jan 2006"}} {{event.note | truncate:4}}`,
	`{{event.items[].name}} {{event.items.0.price | number:2}} {{contact.city | upper | urlencode}}`,
	`{{- contact.email -}} {{campaign.name}} {{links.unsubscribe}} {{survey.12}} {{reco.1.name}} {{reco.count}}`,
	`{{block.footer}} {{ block.header }} {{block.x}}`,
	`{{if .Attrs.vip}}VIP{{else}}{{.Attrs.first_name}}{{end}}`,
	`{{range .Attrs.products}}{{.}}{{end}}`,
	`<a href="{{links.unsubscribe}}">{{contact.first_name | default:&quot;x&quot;}}</a>`,
	`{"name": "{{contact.first_name}}", "cart": {{event.items | json}}}`,
	`{{contact.first_name | default:"unterminated}}`,
	`{{event.x | nope}}`, `{{`, `}}`, `{{contact.}}`, `{{'a'}}`,
}

// FuzzTranslate runs any template text through every entry point: nothing may panic or hang, and
// a template Translate accepts must translate again the same way.
func FuzzTranslate(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s, false)
		f.Add(s, true)
	}
	v := vars()
	dot := legacy{v.Contact, v.Contact.Attrs}
	f.Fuzz(func(t *testing.T, src string, unescape bool) {
		out, refs, err := Translate(src, unescape)
		if err == nil {
			out2, refs2, err2 := Translate(src, unescape)
			if err2 != nil || out2 != out || len(refs2) != len(refs) {
				t.Fatalf("Translate is not deterministic for %q", src)
			}
		}
		Refs(src)
		SurveyIDs(src)
		UsesReco(src)
		_ = Check("f", src, false)
		_ = Check("f", src, true)
		r := New(v)
		_, _ = r.Text("f", src, dot)
		_, _ = r.HTML("f", src, dot)
		_, _ = r.JSON("f", src, dot)
		r.Missing()
	})
}

// FuzzExpandBlocks checks block expansion: nothing panics and the keys it reports are valid.
func FuzzExpandBlocks(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s, "<p>{{contact.first_name}}</p>{{block.footer}}")
	}
	f.Fuzz(func(t *testing.T, src, block string) {
		keys := BlockKeys(src)
		for _, k := range keys {
			if !BlockKeyRe.MatchString(k) {
				t.Fatalf("BlockKeys(%q) returned invalid key %q", src, k)
			}
		}
		blocks := map[string]string{"footer": block, "header": block}
		for _, k := range keys {
			blocks[k] = block
		}
		ExpandBlocks(src, blocks)
	})
}
