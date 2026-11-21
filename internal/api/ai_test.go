package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

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
