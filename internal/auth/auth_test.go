package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Errorf("unexpected encoding %q", h)
	}
	if !CheckPassword(h, "correct horse battery") {
		t.Error("correct password rejected")
	}
	if CheckPassword(h, "correct horse batterY") {
		t.Error("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("two hashes of one password are equal: salt is not random")
	}
	if _, err := HashPassword("short"); err != ErrWeakPassword {
		t.Errorf("short password err = %v", err)
	}
	for _, bad := range []string{"", "$argon2id$v=19$m=1$x$y", "$bcrypt$...", "$argon2id$v=19$m=65536,t=2,p=2$!!$!!"} {
		if CheckPassword(bad, "anything long") {
			t.Errorf("malformed hash %q accepted", bad)
		}
	}
}

func TestTokens(t *testing.T) {
	a, b := NewToken("ps_"), NewToken("ps_")
	if a == b || !strings.HasPrefix(a, "ps_") || len(a) != 3+48 {
		t.Errorf("tokens %q %q", a, b)
	}
	if HashToken(a) == HashToken(b) || len(HashToken(a)) != 64 {
		t.Error("token hashes collide or have the wrong length")
	}
}

func TestBox(t *testing.T) {
	if _, err := NewBox("abcd"); err == nil {
		t.Error("short key accepted")
	}
	box, err := NewBox(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed := box.Seal([]byte("client-secret"))
	if strings.Contains(string(sealed), "client-secret") {
		t.Fatal("plaintext visible in sealed value")
	}
	plain, err := box.Open(sealed)
	if err != nil || string(plain) != "client-secret" {
		t.Fatalf("open = %q, %v", plain, err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := box.Open(sealed); err == nil {
		t.Error("tampered value opened")
	}
	other, _ := NewBox(strings.Repeat("cd", 32))
	s := box.SealString("state|nonce")
	if _, err := other.OpenString(s); err == nil {
		t.Error("value opened with the wrong key")
	}
	if got, err := box.OpenString(s); err != nil || got != "state|nonce" {
		t.Errorf("OpenString = %q, %v", got, err)
	}
}
