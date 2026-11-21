package forms

import (
	"strings"
	"testing"

	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/model"
)

func form() model.Form {
	return model.Form{ID: 4, Name: "Webinar", Fields: []model.FormField{
		{Key: "email", Label: "Email", Type: "email", Required: true},
		{Key: "first_name", Label: "First name", Type: "text", Required: true, Progress: true},
		{Key: "team_size", Label: "Team size", Type: "select", Options: []string{"1-10", "11-100"}, Progress: true},
		{Key: "phone", Label: "Phone", Type: "tel"},
		{Key: "seats", Label: "Seats", Type: "number"},
		{Key: "utm_source", Type: "hidden", Default: "site"},
	}, Settings: model.FormSettings{ListIDs: []int64{12}, Tags: []string{"webinar"}, ConsentText: "Email me", Resubscribe: []string{"email"}}}
}

func TestValidate(t *testing.T) {
	ok := func(id int64) bool { return id == 12 }
	if err := Validate(form(), ok); err != nil {
		t.Fatal(err)
	}
	bad := form()
	bad.Fields = append(bad.Fields, model.FormField{Key: "optout_email", Label: "x", Type: "checkbox"}, model.FormField{Key: "first_name", Label: "Again", Type: "text"})
	bad.Settings.ListIDs = []int64{99}
	err := Validate(bad, ok)
	for _, want := range []string{"managed by Pulse", "used twice", "list 99"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v should mention %q", err, want)
		}
	}
	noID := form()
	noID.Fields = noID.Fields[1:3]
	noID.Fields = append(noID.Fields[:0:0], noID.Fields...)
	if err := Validate(noID, ok); err == nil || !strings.Contains(err.Error(), "email or phone") {
		t.Errorf("a form must identify the submitter: %v", err)
	}
	optIn := form()
	optIn.Settings.ConsentText = ""
	if Validate(optIn, ok) == nil {
		t.Error("opting in without consent text must be refused")
	}
}

func TestParseAndEvents(t *testing.T) {
	f := form()
	sub, errs := Parse(f, f.Fields, map[string]string{"email": "Asha@Example.com", "first_name": "Asha", "team_size": "11-100",
		"phone": "+91 98123 45678", "seats": "3", ConsentField: "on"})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if sub.Email != "asha@example.com" || sub.Attrs["phone"] != "+919812345678" || sub.Attrs["seats"] != 3.0 || sub.Attrs["utm_source"] != "site" {
		t.Fatalf("submission = %+v", sub)
	}
	if _, ok := sub.Attrs["email"]; ok {
		t.Error("email goes to the contact's email, not an attribute")
	}
	evs := Events(f, sub, "asha@example.com", "anon-1", map[string]any{"source": "hosted"}, "")
	names := []string{}
	for _, e := range evs {
		names = append(names, e.Name)
	}
	want := []string{event.Identify, consent.Resubscribed, event.ListJoined, event.TagAdded, Submitted}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", names, want)
	}
	if evs[0].Props["anonymous_id"] != "anon-1" || evs[0].Email != "asha@example.com" || evs[4].Props["form_id"] != int64(4) {
		t.Errorf("identify %+v / submitted %+v", evs[0], evs[4])
	}

	_, errs = Parse(f, f.Fields, map[string]string{"email": "not-an-email", "team_size": "huge", "phone": "98123", "seats": "x"})
	got := map[string]bool{}
	for _, e := range errs {
		got[e.Field] = true
	}
	for _, k := range []string{"email", "first_name", "team_size", "phone", "seats", ConsentField} {
		if !got[k] {
			t.Errorf("expected an error for %s, got %+v", k, errs)
		}
	}
}

func TestProgressiveProfiling(t *testing.T) {
	f := form()
	known := Known(model.Contact{Email: "a@b.co", Attrs: map[string]any{"first_name": "Asha", "team_size": ""}})
	keys := []string{}
	for _, fd := range Visible(f, known) {
		keys = append(keys, fd.Key)
	}
	// first_name is known and progressive: hidden. team_size is empty: shown. email always shows.
	if strings.Join(keys, ",") != "email,team_size,phone,seats,utm_source" {
		t.Errorf("visible = %v", keys)
	}
	if len(Visible(f, nil)) != len(f.Fields) {
		t.Error("an unknown visitor sees every field")
	}
}

func TestPagesAndFiles(t *testing.T) {
	email := model.FormField{Key: "email", Label: "Email", Type: "email", Required: true}
	brk := func(title string) model.FormField { return model.FormField{Type: TypePageBreak, Label: title} }
	cv := model.FormField{Key: "cv", Label: "CV", Type: TypeFile, Accept: []string{".pdf"}, MaxMB: 2}
	ok := func(int64) bool { return true }
	if err := Validate(model.Form{Name: "x", Fields: []model.FormField{email, brk("More"), cv}}, ok); err != nil {
		t.Fatalf("valid multi-page form: %v", err)
	}
	for name, fs := range map[string][]model.FormField{
		"first":     {brk(""), email},
		"last":      {email, brk("")},
		"twice":     {email, brk(""), brk(""), cv},
		"svg":       {email, {Key: "logo", Label: "Logo", Type: TypeFile, Accept: []string{".svg"}}},
		"too big":   {email, {Key: "cv", Label: "CV", Type: TypeFile, MaxMB: 50}},
		"file mail": {{Key: "phone", Label: "P", Type: "tel"}, {Key: "email", Label: "E", Type: TypeFile}},
	} {
		if err := Validate(model.Form{Name: "x", Fields: fs}, ok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// Progressive profiling can empty a page: its break goes too.
	city := model.FormField{Key: "city", Label: "City", Type: "text", Progress: true}
	f := model.Form{Fields: []model.FormField{email, brk("Where"), city, brk("Files"), cv}}
	if got := Pages(Visible(f, map[string]bool{"city": true})); len(got) != 2 || got[1].Title != "Files" || got[1].Fields[0].Key != "cv" {
		t.Errorf("pages without city: %+v", got)
	}
	if got := Pages(Visible(f, nil)); len(got) != 3 || got[0].Title != "" || got[1].Title != "Where" {
		t.Errorf("pages: %+v", got)
	}

	files, errs := ParseFiles([]model.FormField{cv}, map[string]Upload{"cv": {Filename: "a/b/cv.PDF", Data: []byte("%PDF-1.4 x")}})
	if len(errs) != 0 || len(files) != 1 || files[0].Filename != "cv.PDF" || files[0].ContentType != "application/pdf" {
		t.Fatalf("pdf: %+v %+v", files, errs)
	}
	for name, u := range map[string]Upload{
		"renamed html": {Filename: "cv.pdf", Data: []byte("<html><body>hi</body></html>")},
		"wrong type":   {Filename: "cv.docx", Data: []byte("PK\x03\x04")},
		"too big":      {Filename: "cv.pdf", Data: append([]byte("%PDF-"), make([]byte, 2<<20)...)},
	} {
		if _, errs := ParseFiles([]model.FormField{cv}, map[string]Upload{"cv": u}); len(errs) != 1 {
			t.Errorf("%s: accepted", name)
		}
	}
	// Parse ignores files and page breaks: they never become attributes.
	sub, errs2 := Parse(f, Visible(f, nil), map[string]string{"email": "a@b.co", "cv": "x"})
	if len(errs2) != 0 || sub.Attrs["cv"] != nil {
		t.Errorf("parse: %+v %v", sub, errs2)
	}
}
