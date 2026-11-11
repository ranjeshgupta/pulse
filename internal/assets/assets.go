// Package assets holds the rules for uploaded files (downloads and email attachments): which types
// are allowed and how they are recognised, size caps, safe file names, and the sealed tokens in
// short-lived download links.
//
// A file's type comes from its extension, and files whose extension promises a binary format must
// start with that format's signature, so a renamed HTML page can't pass as a PDF. Downloads are
// always served as attachments with the stored type (never rendered inline), so SVG and other
// types a browser could execute are safe to offer.
package assets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"pulse/internal/auth"
)

const (
	MaxSize = 25 << 20 // per file
	// MaxWorkspaceBytes caps a workspace's assets in total (they live in Postgres).
	MaxWorkspaceBytes = 2 << 30
	// MaxAttachmentBytes caps one email's attachments together (channel.MaxAttachments caps their number).
	MaxAttachmentBytes = 10 << 20
)

type kind struct {
	contentType string
	magic       [][]byte // the file must start with one of these (none: text, checked as UTF-8)
	text        bool
}

var (
	zipMagic = [][]byte{[]byte("PK\x03\x04"), []byte("PK\x05\x06")}
	kinds    = map[string]kind{
		".pdf":  {contentType: "application/pdf", magic: [][]byte{[]byte("%PDF-")}},
		".png":  {contentType: "image/png", magic: [][]byte{[]byte("\x89PNG\r\n\x1a\n")}},
		".jpg":  {contentType: "image/jpeg", magic: [][]byte{{0xff, 0xd8, 0xff}}},
		".jpeg": {contentType: "image/jpeg", magic: [][]byte{{0xff, 0xd8, 0xff}}},
		".gif":  {contentType: "image/gif", magic: [][]byte{[]byte("GIF87a"), []byte("GIF89a")}},
		".webp": {contentType: "image/webp", magic: [][]byte{[]byte("RIFF")}},
		".svg":  {contentType: "image/svg+xml", text: true},
		".csv":  {contentType: "text/csv", text: true},
		".txt":  {contentType: "text/plain", text: true},
		".ics":  {contentType: "text/calendar", text: true},
		".zip":  {contentType: "application/zip", magic: zipMagic},
		".docx": {contentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", magic: zipMagic},
		".xlsx": {contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", magic: zipMagic},
		".pptx": {contentType: "application/vnd.openxmlformats-officedocument.presentationml.presentation", magic: zipMagic},
	}
)

// Extensions lists the allowed file extensions, for messages and the upload picker.
func Extensions() []string {
	return []string{".pdf", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".csv", ".txt", ".ics", ".zip", ".docx", ".xlsx", ".pptx"}
}

// ErrType is returned for files Pulse doesn't accept.
var ErrType = errors.New("this file type isn't allowed: upload PDF, an image (PNG, JPEG, GIF, WebP, SVG), CSV, TXT, ICS, ZIP, DOCX, XLSX or PPTX")

// Detect returns the content type for a file, from its extension, after checking that the content
// matches it. data may be just the first bytes for binary types; text types need the whole file.
func Detect(filename string, data []byte) (string, error) {
	k, ok := kinds[strings.ToLower(path.Ext(filename))]
	if !ok {
		return "", ErrType
	}
	if len(data) == 0 {
		return "", errors.New("the file is empty")
	}
	if k.text {
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return "", fmt.Errorf("%s files must be UTF-8 text", strings.TrimPrefix(path.Ext(filename), "."))
		}
		if k.contentType != "image/svg+xml" {
			return k.contentType + "; charset=utf-8", nil
		}
		return k.contentType, nil
	}
	for _, m := range k.magic {
		if bytes.HasPrefix(data, m) {
			if k.contentType == "image/webp" && (len(data) < 12 || string(data[8:12]) != "WEBP") {
				break
			}
			return k.contentType, nil
		}
	}
	return "", fmt.Errorf("the file's content isn't a %s file", strings.ToUpper(strings.TrimPrefix(path.Ext(filename), ".")))
}

// SafeFilename keeps the last path element of an uploaded name, without control characters or
// quotes, at most 120 characters, and keeps its extension.
func SafeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' || r == '/' || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.Trim(name, "."))
	if name == "" || name == "/" {
		return ""
	}
	if r := []rune(name); len(r) > 120 {
		ext := path.Ext(name)
		name = string(r[:120-len([]rune(ext))]) + ext
	}
	return name
}

// Disposition is the Content-Disposition header that makes browsers save the file under its name.
func Disposition(filename string) string {
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); v != "" {
		return v
	}
	return "attachment"
}

// Token is what a short-lived download link carries. Purpose "gate" links are issued after a form
// submission and record an asset_downloaded event for ExternalID; "attach" links are email
// attachments handed to the custom HTTP gateway (no event, no download count).
type Token struct {
	TenantID   int64  `json:"t"`
	AssetID    int64  `json:"a"`
	ExternalID string `json:"e,omitempty"`
	Purpose    string `json:"p"`
	Expires    int64  `json:"x"` // unix seconds
}

const (
	PurposeGate   = "gate"
	PurposeAttach = "attach"
)

// tokenPrefix keeps download tokens apart from everything else sealed with the same key (tracking
// links, SSO state): neither can be passed off as the other.
const tokenPrefix = "asset:"

// Seal encrypts a token (AES-GCM, so it can't be read or forged).
func Seal(box *auth.Box, t Token) string {
	b, _ := json.Marshal(t)
	return box.SealString(tokenPrefix + string(b))
}

// Open decrypts a token and checks that it hasn't expired.
func Open(box *auth.Box, s string, now time.Time) (Token, error) {
	var t Token
	if box == nil || s == "" {
		return t, errors.New("invalid download link")
	}
	plain, err := box.OpenString(s)
	body, ok := strings.CutPrefix(plain, tokenPrefix)
	if err != nil || !ok || json.Unmarshal([]byte(body), &t) != nil || (t.Purpose != PurposeGate && t.Purpose != PurposeAttach) {
		return Token{}, errors.New("invalid download link")
	}
	if now.Unix() > t.Expires {
		return Token{}, errors.New("this download link has expired")
	}
	return t, nil
}

// UploadExtensions are the types a form's file field may accept: documents, images and text,
// but not SVG (it can carry script), ICS or ZIP.
func UploadExtensions() []string {
	return []string{".pdf", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".txt", ".csv", ".docx", ".xlsx", ".pptx"}
}

// DetectUpload checks a file sent through a form: its extension must be among accept (or any
// upload type when accept is empty), its content must carry that format's signature (Detect), and
// content sniffing (the WHATWG algorithm browsers use) must not see a web page or script in it,
// so a renamed HTML or XML file never passes as text. It returns the content type to store.
func DetectUpload(filename string, data []byte, accept []string) (string, error) {
	ext := strings.ToLower(path.Ext(filename))
	allowed := UploadExtensions()
	if len(accept) > 0 {
		allowed = accept
	}
	ok := false
	for _, a := range allowed {
		a = strings.ToLower(a)
		ok = ok || a == ext || (a == ".jpg" && ext == ".jpeg") || (a == ".jpeg" && ext == ".jpg")
	}
	if !ok || !slicesContains(UploadExtensions(), ext) {
		names := make([]string, len(allowed))
		for i, a := range allowed {
			names[i] = strings.ToUpper(strings.TrimPrefix(a, "."))
		}
		return "", fmt.Errorf("this type of file isn't accepted here: upload %s", strings.Join(names, ", "))
	}
	ct, err := Detect(filename, data)
	if err != nil {
		return "", err
	}
	sniffed := http.DetectContentType(data)
	for _, bad := range []string{"text/html", "text/xml", "application/xml", "application/javascript", "image/svg+xml"} {
		if strings.HasPrefix(sniffed, bad) {
			return "", errors.New("the file's content looks like a web page or script, not a " + strings.ToUpper(strings.TrimPrefix(ext, ".")) + " file")
		}
	}
	if k := kinds[ext]; !k.text && sniffed != "application/octet-stream" && !sniffMatches(k.contentType, sniffed) {
		return "", fmt.Errorf("the file's content isn't a %s file", strings.ToUpper(strings.TrimPrefix(ext, ".")))
	}
	return ct, nil
}

// sniffMatches: content sniffing agrees with the extension's type (Office files are ZIP archives).
func sniffMatches(want, sniffed string) bool {
	if strings.HasPrefix(want, "application/vnd.openxmlformats") {
		return sniffed == "application/zip"
	}
	return strings.HasPrefix(sniffed, want)
}

func slicesContains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
