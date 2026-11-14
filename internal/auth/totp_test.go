package auth

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B, the SHA-1 rows: seed "12345678901234567890", 8 digits.
func TestTOTPRFC6238Vectors(t *testing.T) {
	seed := []byte("12345678901234567890")
	for _, v := range []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	} {
		if got := hotp(seed, uint64(TOTPStep(time.Unix(v.unix, 0))), 8); got != v.want {
			t.Errorf("T=%d: %s, want %s", v.unix, got, v.want)
		}
	}
	// The 6-digit code is the last six digits of the same value.
	if got := TOTPCode(seed, TOTPStep(time.Unix(59, 0))); got != "287082" {
		t.Errorf("6 digits at T=59: %s", got)
	}
}

func TestTOTPMatchWindowAndReplay(t *testing.T) {
	secret := NewTOTPSecret()
	now := time.Unix(1_700_000_000, 0)
	cur := TOTPStep(now)
	for _, d := range []int64{-1, 0, 1} {
		code := TOTPCode(secret, cur+d)
		step, ok := TOTPMatch(secret, code[:3]+" "+code[3:], now, 0)
		if !ok || step != cur+d {
			t.Errorf("step %+d: ok=%v step=%d", d, ok, step)
		}
	}
	for _, d := range []int64{-2, 2} {
		if _, ok := TOTPMatch(secret, TOTPCode(secret, cur+d), now, 0); ok {
			t.Errorf("step %+d should be outside the window", d)
		}
	}
	// A step at or before the last one used never matches again.
	if _, ok := TOTPMatch(secret, TOTPCode(secret, cur), now, cur); ok {
		t.Error("a used code matched again")
	}
	if _, ok := TOTPMatch(secret, TOTPCode(secret, cur+1), now, cur); !ok {
		t.Error("the next step should still match")
	}
	if _, ok := TOTPMatch(secret, "12345", now, 0); ok {
		t.Error("short code matched")
	}
}

func TestTOTPURIAndSecretText(t *testing.T) {
	secret := []byte("12345678901234567890")
	uri := TOTPURI("Pulse", "asha@example.com (Acme Retail)", secret)
	for _, want := range []string{"otpauth://totp/Pulse:asha@example.com%20%28Acme%20Retail%29?", "secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "issuer=Pulse", "digits=6", "period=30"} {
		if !strings.Contains(uri, want) {
			t.Errorf("uri %s lacks %s", uri, want)
		}
	}
	if got := TOTPSecretText(secret); got != "GEZD GNBV GY3T QOJQ GEZD GNBV GY3T QOJQ" {
		t.Errorf("secret text %q", got)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes := NewRecoveryCodes(RecoveryCodes)
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' || seen[c] {
			t.Errorf("bad or repeated code %q", c)
		}
		seen[c] = true
		if LooksLikeTOTP(c) {
			t.Errorf("%q looks like an app code", c)
		}
		// People may type it without the dash, in capitals, with spaces.
		if HashRecoveryCode(strings.ToUpper(strings.ReplaceAll(c, "-", " "))) != HashRecoveryCode(c) {
			t.Errorf("%q: hash depends on formatting", c)
		}
	}
	if !LooksLikeTOTP("123 456") || LooksLikeTOTP("12345a") {
		t.Error("LooksLikeTOTP")
	}
}

func TestTwoFactorPolicy(t *testing.T) {
	var p TwoFactorPolicy
	if err := p.Normalize(); err != nil || p.Mode != TwoFactorOptional || p.Requires(1, RoleAdmin) {
		t.Errorf("default: %+v %v", p, err)
	}
	p = TwoFactorPolicy{Mode: TwoFactorEveryone, Roles: []string{"editor"}}
	_ = p.Normalize()
	if !p.Requires(9, RoleViewer) || len(p.Roles) != 0 {
		t.Errorf("everyone: %+v", p)
	}
	p = TwoFactorPolicy{Mode: TwoFactorSome, Roles: []string{RoleAdmin, "role:4"}, Users: []int64{7}}
	if !p.Requires(1, RoleAdmin) || !p.Requires(2, "role:4") || !p.Requires(7, RoleViewer) || p.Requires(8, RoleEditor) {
		t.Errorf("some: %+v", p)
	}
	if err := (&TwoFactorPolicy{Mode: "sometimes"}).Normalize(); err == nil {
		t.Error("unknown mode accepted")
	}
}
