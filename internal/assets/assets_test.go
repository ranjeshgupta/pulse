package assets

import (
	"strings"
	"testing"
	"time"

	"pulse/internal/auth"
)

func TestDetect(t *testing.T) {
	pdf := []byte("%PDF-1.7\n...")
	png := []byte("\x89PNG\r\n\x1a\n....")
	zip := []byte("PK\x03\x04....")
	for _, tc := range []struct {
		name string
		data []byte
		want string // content type, or an error substring prefixed with "!"
	}{
		{"guide.pdf", pdf, "application/pdf"},
		{"GUIDE.PDF", pdf, "application/pdf"},
		{"logo.png", png, "image/png"},
		{"report.docx", zip, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"data.csv", []byte("a,b\n1,2\n"), "text/csv; charset=utf-8"},
		{"invite.ics", []byte("BEGIN:VCALENDAR"), "text/calendar; charset=utf-8"},
		{"icon.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), "image/svg+xml"},
		{"photo.webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "image/webp"},
		{"page.html", []byte("<html>"), "!isn't allowed"},
		{"run.exe", []byte("MZ"), "!isn't allowed"},
		{"noext", pdf, "!isn't allowed"},
		{"fake.pdf", []byte("<html><script>"), "!isn't a PDF"},
		{"fake.png", pdf, "!isn't a PNG"},
		{"wav.webp", []byte("RIFF\x00\x00\x00\x00WAVEfmt "), "!isn't a WEBP"},
		{"bin.txt", []byte("a\x00b"), "!UTF-8"},
		{"empty.pdf", nil, "!empty"},
	} {
		got, err := Detect(tc.name, tc.data)
		if strings.HasPrefix(tc.want, "!") {
			if err == nil || !strings.Contains(err.Error(), tc.want[1:]) {
				t.Errorf("%s: got %q, err %v; want error %q", tc.name, got, err, tc.want[1:])
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, err %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestSafeFilename(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":                      "report.pdf",
		"../../etc/passwd.txt":            "passwd.txt",
		`C:\Users\asha\Q3 "final".xlsx`:   "Q3 final.xlsx",
		"line\r\nbreak.csv":               "linebreak.csv",
		"..":                              "",
		"résumé.pdf":                      "résumé.pdf",
		strings.Repeat("a", 200) + ".pdf": strings.Repeat("a", 116) + ".pdf",
	} {
		if got := SafeFilename(in); got != want {
			t.Errorf("SafeFilename(%q) = %q, want %q", in, got, want)
		}
	}
	if d := Disposition("Q3 report.pdf"); d != `attachment; filename="Q3 report.pdf"` {
		t.Errorf("disposition = %s", d)
	}
	if d := Disposition("résumé.pdf"); d != `attachment; filename*=utf-8''r%C3%A9sum%C3%A9.pdf` {
		t.Errorf("non-ASCII disposition = %s", d)
	}
}

func TestTokens(t *testing.T) {
	box, _ := auth.NewBox(strings.Repeat("ab", 32))
	now := time.Unix(1_800_000_000, 0)
	tok := Token{TenantID: 1, AssetID: 7, ExternalID: "asha@example.com", Purpose: PurposeGate, Expires: now.Add(15 * time.Minute).Unix()}
	s := Seal(box, tok)
	if strings.Contains(s, "asha") {
		t.Error("the token is readable")
	}
	got, err := Open(box, s, now)
	if err != nil || got != tok {
		t.Fatalf("open = %+v, %v", got, err)
	}
	if _, err := Open(box, s, now.Add(16*time.Minute)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired token: %v", err)
	}
	if _, err := Open(box, s[:len(s)-2]+"xx", now); err == nil {
		t.Error("a tampered token opened")
	}
	// Other things sealed with the same key (tracking links, secrets) aren't download tokens.
	if _, err := Open(box, box.SealString(`{"t":1,"a":7,"p":"gate","x":1900000000}`), now); err == nil {
		t.Error("a value sealed for something else opened as a download token")
	}
	if _, err := Open(nil, s, now); err == nil {
		t.Error("opened without a key")
	}
}
