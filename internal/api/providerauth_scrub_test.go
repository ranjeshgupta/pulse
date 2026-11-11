package api

import (
	"strings"
	"testing"
)

func TestScrubSecrets(t *testing.T) {
	sec := map[string]string{"password": `p@ss "word"/1`, "k": "ab"}
	msg := `login failed: 400 {"echo":"p@ss \"word\"/1"} url=/x?p=p%40ss+%22word%22%2F1 path=/p@ss%20%22word%22%2F1`
	got := scrubSecrets(msg, sec)
	if strings.Contains(got, "word") {
		t.Errorf("secret left in %q", got)
	}
}
