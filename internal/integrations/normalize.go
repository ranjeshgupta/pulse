package integrations

// Normalising and hashing contact identifiers for ad platforms. Both Facebook and Google match
// SHA-256 hashes of a normalised email (trimmed, lower case) and phone number; they differ only in
// how the number is written before hashing (Google: E.164 with "+", Facebook: digits only).

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// NormalizeEmail trims and lower-cases an address; "" when it isn't one.
func NormalizeEmail(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	at := strings.LastIndexByte(s, '@')
	if at < 1 || at == len(s)-1 || strings.ContainsAny(s, " \t\r\n") {
		return ""
	}
	return s
}

// googleEmail applies Google's extra rule: dots in the name of gmail.com and googlemail.com
// addresses don't count.
func googleEmail(s string) string {
	name, domain, ok := strings.Cut(s, "@")
	if ok && (domain == "gmail.com" || domain == "googlemail.com") {
		return strings.ReplaceAll(name, ".", "") + "@" + domain
	}
	return s
}

// E164 writes a phone number as +<country><number>. Numbers without "+" (or a 00 prefix) get the
// default country code (digits only, e.g. "91"); without one they can't be normalised and "" is
// returned. A leading trunk 0 of a national number is dropped.
func E164(phone, defaultCountry string) string {
	s := strings.TrimSpace(phone)
	if s == "" {
		return ""
	}
	intl := strings.HasPrefix(s, "+")
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if !intl && strings.HasPrefix(digits, "00") {
		intl, digits = true, digits[2:]
	}
	if !intl {
		cc := strings.TrimPrefix(strings.TrimSpace(defaultCountry), "+")
		if cc == "" || strings.Trim(cc, "0123456789") != "" {
			return ""
		}
		digits = cc + strings.TrimLeft(digits, "0")
	}
	if len(digits) < 8 || len(digits) > 15 || digits[0] == '0' {
		return ""
	}
	return "+" + digits
}

// Sha256Hex is the lower-case hex SHA-256 of s.
func Sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Identity is a contact's hashed identifiers for one platform ("" when unknown).
type Identity struct {
	ContactID uint32
	EmailHash string
	PhoneHash string
}

// Empty reports whether there is nothing to match on.
func (i Identity) Empty() bool { return i.EmailHash == "" && i.PhoneHash == "" }

// HashFor builds the identity a platform matches: kind is facebook_audiences or google_ads.
func HashFor(kind, email, phone, defaultCountry string) Identity {
	var id Identity
	if e := NormalizeEmail(email); e != "" {
		if kind == KindGoogleAds {
			e = googleEmail(e)
		}
		id.EmailHash = Sha256Hex(e)
	}
	if p := E164(phone, defaultCountry); p != "" {
		if kind == KindFacebook {
			p = strings.TrimPrefix(p, "+")
		}
		id.PhoneHash = Sha256Hex(p)
	}
	return id
}
