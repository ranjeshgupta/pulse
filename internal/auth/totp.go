package auth

// Two-step sign-in with an authenticator app (Google Authenticator, Microsoft Authenticator, Authy,
// 1Password…): time-based one-time passwords (RFC 6238, HMAC-SHA1, 30-second steps, 6 digits),
// and the one-time recovery codes people keep for when they lose their phone.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	totpPeriod  = 30 // seconds per step
	totpDigits  = 6
	totpSkew    = 1  // steps accepted either side of now: a phone clock up to ~30 s off still works
	secretBytes = 20 // 160 bits, the size RFC 4226 recommends for HMAC-SHA1

	// RecoveryCodes is how many recovery codes a person gets at a time.
	RecoveryCodes = 10
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random secret for an authenticator app.
func NewTOTPSecret() []byte {
	b := make([]byte, secretBytes)
	_, _ = rand.Read(b)
	return b
}

// TOTPSecretText is the secret as people type it into an app: base32, in groups of four.
func TOTPSecretText(secret []byte) string {
	s := b32.EncodeToString(secret)
	var out []string
	for len(s) > 4 {
		out = append(out, s[:4])
		s = s[4:]
	}
	return strings.Join(append(out, s), " ")
}

// TOTPURI is the otpauth:// address the QR code holds. Apps show "issuer (account)".
func TOTPURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", b32.EncodeToString(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPStep is the 30-second step a time falls in.
func TOTPStep(t time.Time) int64 { return t.Unix() / totpPeriod }

// hotp is RFC 4226: HMAC-SHA1 of the counter, dynamically truncated to digits decimal digits.
func hotp(secret []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, v%mod)
}

// TOTPCode is the 6-digit code for a step.
func TOTPCode(secret []byte, step int64) string { return hotp(secret, uint64(step), totpDigits) }

// TOTPMatch checks a code against the steps around now and returns the step it matched. Steps at
// or before last (the last code accepted) never match, so a code can't be used twice.
func TOTPMatch(secret []byte, code string, now time.Time, last int64) (int64, bool) {
	code = CleanCode(code)
	if len(code) != totpDigits {
		return 0, false
	}
	cur := TOTPStep(now)
	for step := cur - totpSkew; step <= cur+totpSkew; step++ {
		if step <= last {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(TOTPCode(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// CleanCode drops the spaces and dashes people type or paste into codes, and lowercases letters.
func CleanCode(code string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, code))
}

// recoveryAlphabet has no look-alikes (0/o, 1/l/i), so codes read back from paper type correctly.
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// NewRecoveryCodes returns n codes like "k7wq2-mx9dp" (10 characters, about 50 bits each).
func NewRecoveryCodes(n int) []string {
	out := make([]string, n)
	limit := byte(256 / len(recoveryAlphabet) * len(recoveryAlphabet)) // no modulo bias
	for i := range out {
		b := make([]byte, 0, 10)
		var r [1]byte
		for len(b) < 10 {
			_, _ = rand.Read(r[:])
			if r[0] < limit {
				b = append(b, recoveryAlphabet[int(r[0])%len(recoveryAlphabet)])
			}
		}
		out[i] = string(b[:5]) + "-" + string(b[5:])
	}
	return out
}

// HashRecoveryCode is how a recovery code is stored and looked up (SHA-256 of the cleaned code;
// the codes are random enough that a slow hash adds nothing).
func HashRecoveryCode(code string) string { return HashToken("rc:" + CleanCode(code)) }

// Two-step sign-in policies of a workspace.
const (
	TwoFactorOptional = "optional" // people choose (the default)
	TwoFactorEveryone = "everyone" // required for everyone who signs in with a password
	TwoFactorSome     = "some"     // required for the listed roles and people
)

// TwoFactorPolicy says who must use two-step sign-in. It applies to password sign-ins only: with
// single sign-on the identity provider decides about extra steps.
type TwoFactorPolicy struct {
	Mode  string   `json:"mode"`
	Roles []string `json:"roles"` // "admin", "editor", "viewer" or "role:<id>"
	Users []int64  `json:"users"`
}

// Requires reports whether the policy requires two-step sign-in of a user with this id and role.
func (p TwoFactorPolicy) Requires(user int64, role string) bool {
	switch p.Mode {
	case TwoFactorEveryone:
		return true
	case TwoFactorSome:
		for _, r := range p.Roles {
			if r == role {
				return true
			}
		}
		for _, u := range p.Users {
			if u == user {
				return true
			}
		}
	}
	return false
}

// Normalize fills the defaults and drops lists the mode doesn't use; it fails on an unknown mode.
func (p *TwoFactorPolicy) Normalize() error {
	switch p.Mode {
	case "":
		p.Mode = TwoFactorOptional
	case TwoFactorOptional, TwoFactorEveryone, TwoFactorSome:
	default:
		return fmt.Errorf("unknown two-step sign-in policy %q (optional, everyone or some)", p.Mode)
	}
	if p.Mode != TwoFactorSome || p.Roles == nil {
		p.Roles = []string{}
	}
	if p.Mode != TwoFactorSome || p.Users == nil {
		p.Users = []int64{}
	}
	return nil
}

// LooksLikeTOTP reports whether a code is six digits (an app code) rather than a recovery code.
func LooksLikeTOTP(code string) bool {
	c := CleanCode(code)
	if len(c) != totpDigits {
		return false
	}
	for _, r := range c {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
