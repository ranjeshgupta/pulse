package tracking

// Message references: a short, signed id that every outgoing email carries (X-Pulse-Ref, the
// Message-ID's local part, and the sending service's own metadata header), so bounces, complaints
// and delivery reports — from provider webhooks or from bounce emails — can be matched to the
// message, its campaign (broadcast or journey step) and its template.
//
// A reference is "p1" + base64url(varint fields) + "." + base64url(HMAC-SHA256[:10]), usually
// 45-70 characters: short enough for Postmark metadata (80) and SES tags (256), and only made of
// characters every service accepts (letters, digits, - _ .). It is signed, not sealed: nothing in
// it is personal (ids only), and a forged or altered reference is refused.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Ref is what a message reference identifies.
type Ref struct {
	TenantID    int64
	ContactID   uint32
	MessageID   string
	TemplateID  int64
	JourneyID   int64
	BroadcastID int64
	Run         int64
	Variant     string
}

// Props are the message's identifiers as event properties (as Token.Props).
func (r Ref) Props() map[string]any {
	p := map[string]any{"message_id": r.MessageID, "template_id": r.TemplateID, "journey_id": r.JourneyID}
	if r.BroadcastID != 0 {
		p["broadcast_id"], p["run"] = r.BroadcastID, r.Run
		if r.Variant != "" {
			p["variant"] = r.Variant
		}
	}
	return p
}

const refPrefix = "p1"

var errBadRef = errors.New("invalid message reference")

func (c *Codec) refMAC(payload string) string {
	m := hmac.New(sha256.New, c.mac)
	m.Write([]byte("ref|" + payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:10])
}

// broadcastMessageID is how broadcasts name their messages (broadcast.Runner.message); such ids
// aren't stored in the reference, they are rebuilt.
func broadcastMessageID(r Ref) string {
	return fmt.Sprintf("bc%d:%d:%d", r.BroadcastID, r.Run, r.ContactID)
}

// Ref encodes a message reference.
func (c *Codec) Ref(r Ref) string {
	var b []byte
	for _, n := range []uint64{uint64(r.TenantID), uint64(r.ContactID), uint64(r.TemplateID), uint64(r.JourneyID),
		uint64(r.BroadcastID), uint64(r.Run)} {
		b = binary.AppendUvarint(b, n)
	}
	b = binary.AppendUvarint(b, uint64(len(r.Variant)))
	b = append(b, r.Variant...)
	mid := r.MessageID
	if r.BroadcastID != 0 && mid == broadcastMessageID(r) {
		mid = ""
	}
	b = binary.AppendUvarint(b, uint64(len(mid)))
	b = append(b, mid...)
	payload := refPrefix + base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + c.refMAC(payload)
}

// ParseRef checks and decodes a reference (surrounding <>, whitespace and an @domain, as in a
// Message-ID, are ignored).
func (c *Codec) ParseRef(s string) (Ref, error) {
	s = strings.Trim(strings.TrimSpace(s), "<>")
	if at := strings.IndexByte(s, '@'); at >= 0 {
		s = s[:at]
	}
	payload, sig, ok := strings.Cut(s, ".")
	if !ok || !strings.HasPrefix(payload, refPrefix) || len(s) > 300 || !hmac.Equal([]byte(sig), []byte(c.refMAC(payload))) {
		return Ref{}, errBadRef
	}
	b, err := base64.RawURLEncoding.DecodeString(payload[len(refPrefix):])
	if err != nil {
		return Ref{}, errBadRef
	}
	var nums [6]uint64
	for i := range nums {
		n, k := binary.Uvarint(b)
		if k <= 0 {
			return Ref{}, errBadRef
		}
		nums[i], b = n, b[k:]
	}
	str := func() (string, bool) {
		n, k := binary.Uvarint(b)
		if k <= 0 || uint64(len(b)-k) < n {
			return "", false
		}
		v := string(b[k : k+int(n)])
		b = b[k+int(n):]
		return v, true
	}
	r := Ref{TenantID: int64(nums[0]), ContactID: uint32(nums[1]), TemplateID: int64(nums[2]), JourneyID: int64(nums[3]),
		BroadcastID: int64(nums[4]), Run: int64(nums[5])}
	var ok1, ok2 bool
	r.Variant, ok1 = str()
	r.MessageID, ok2 = str()
	if !ok1 || !ok2 || len(b) != 0 || r.TenantID <= 0 {
		return Ref{}, errBadRef
	}
	if r.MessageID == "" && r.BroadcastID != 0 {
		r.MessageID = broadcastMessageID(r)
	}
	return r, nil
}

// RefFromToken builds a message's reference from its tracking token and contact id.
func RefFromToken(t Token, contact uint32) Ref {
	return Ref{TenantID: t.TenantID, ContactID: contact, MessageID: t.MessageID, TemplateID: t.TemplateID,
		JourneyID: t.JourneyID, BroadcastID: t.BroadcastID, Run: t.Run, Variant: t.Variant}
}

// String is a short description for logs.
func (r Ref) String() string {
	return strconv.FormatInt(r.TenantID, 10) + "/" + r.MessageID
}
