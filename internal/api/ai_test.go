package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/ai"
)

type fakeAI struct{ answer string }

func (f fakeAI) JSON(context.Context, string, string, map[string]any) ([]byte, error) {
	return []byte(f.answer), nil
}

func TestAIHelp(t *testing.T) {
	s, _, _ := newServer()
	h := s.Handler()
	if w := do(t, h, "GET", "/v1/ai/status", "key-1", ""); !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Errorf("status without a key: %s", w.Body)
	}
	if w := do(t, h, "POST", "/v1/ai/rewrite", "key-1", `{"text":"Hi","style":"shorter"}`); w.Code != http.StatusServiceUnavailable {
		t.Errorf("off: %d", w.Code)
	}
	mr := miniredis.RunT(t)
	s.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s.AI = &ai.Helper{C: fakeAI{answer: `{"text":"Hello {{contact.first_name}}"}`}}
	if w := do(t, h, "POST", "/v1/ai/rewrite", "key-1", `{"text":"Hi there {{contact.first_name}}","style":"shorter","channel":"sms"}`); w.Code != 200 || !strings.Contains(w.Body.String(), "Hello") {
		t.Errorf("rewrite: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/ai/rewrite", "key-1", `{"text":"Hi {{contact.last_name}}","style":"shorter"}`); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("changed placeholder: %d %s", w.Code, w.Body)
	}
	s.AI = &ai.Helper{C: fakeAI{answer: `{"rule":"{\"attr\":\"city\",\"op\":\"gt\",\"value\":\"Pune\"}","note":"x"}`}}
	if w := do(t, h, "POST", "/v1/ai/segment", "key-1", `{"description":"people in Pune"}`); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "doesn't work") {
		t.Errorf("invalid AI rule: %d %s", w.Code, w.Body)
	}
	for i := 0; i < aiPerMinute; i++ {
		do(t, h, "POST", "/v1/ai/rewrite", "key-1", `{"text":"x","style":"fix"}`)
	}
	if w := do(t, h, "POST", "/v1/ai/rewrite", "key-1", `{"text":"x","style":"fix"}`); w.Code != http.StatusTooManyRequests {
		t.Errorf("rate limit: %d", w.Code)
	}
}

type recAI struct {
	answer string
	err    error
	user   *string
}

func (f recAI) JSON(_ context.Context, _, user string, _ map[string]any) ([]byte, error) {
	if f.user != nil {
		*f.user = user
	}
	return []byte(f.answer), f.err
}

func TestAIHelpLimits(t *testing.T) {
	s, _, _ := newServer()
	h := s.Handler()
	mr := miniredis.RunT(t)
	s.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	now := time.Date(2026, 11, 21, 9, 0, 30, 0, time.UTC)
	s.now = func() time.Time { return now }
	var user string
	s.AI = &ai.Helper{C: recAI{answer: `{"subjects":["a","b","c"]}`, user: &user}}
	if w := do(t, h, "POST", "/v1/ai/subject-lines", "key-1", `{"content":"Sale","n":3}`); w.Code != 200 || !strings.Contains(user, "Suggest 3 ") {
		t.Errorf("n=3: %d %q", w.Code, user)
	}
	if w := do(t, h, "POST", "/v1/ai/subject-lines", "key-1", `{"content":"Sale"}`); w.Code != 200 || !strings.Contains(user, "Suggest 5 ") {
		t.Errorf("default n: %d %q", w.Code, user)
	}
	// The minute's rate key gets its TTL on every request (a lost EXPIRE doesn't stick).
	k := "airate:1:" + strconv.FormatInt(now.Unix()/60, 10)
	mr.Set(k, "3")
	do(t, h, "POST", "/v1/ai/subject-lines", "key-1", `{"content":"Sale"}`)
	if mr.TTL(k) <= 0 {
		t.Errorf("rate key without TTL")
	}
	// Too long: refused, not cut.
	big := `{"text":"` + strings.Repeat("a", ai.MaxRewrite+1) + `","style":"fix"}`
	if w := do(t, h, "POST", "/v1/ai/rewrite", "key-1", big); w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "too long for AI help") {
		t.Errorf("long rewrite: %d %s", w.Code, w.Body)
	}
	if w := do(t, h, "POST", "/v1/ai/translate", "key-1", `{"fields":{"text":"Hi"},"locale":"Hindi please"}`); w.Code != http.StatusBadRequest {
		t.Errorf("bad locale: %d %s", w.Code, w.Body)
	}
	s.AI = &ai.Helper{C: recAI{err: ai.ErrAnswerTooLong}}
	if w := do(t, h, "POST", "/v1/ai/rewrite", "key-1", `{"text":"Hi","style":"fix"}`); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("answer too long: %d %s", w.Code, w.Body)
	}
	// Daily quota.
	mr.Set("aiday:1:20261121", strconv.Itoa(aiPerDay))
	if w := do(t, h, "POST", "/v1/ai/rewrite", "key-1", `{"text":"Hi","style":"fix"}`); w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "for today") {
		t.Errorf("daily quota: %d %s", w.Code, w.Body)
	}
}
