package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"pulse/internal/model"
)

// auditMem serves ListAudit from a slice, newest (highest id) first, honouring the filter.
type auditMem struct {
	*memStore
	entries []model.AuditEntry
}

func (m *auditMem) InsertAudit(context.Context, int64, model.AuditEntry) error { return nil }
func (m *auditMem) ListAudit(_ context.Context, tenant int64, f model.AuditFilter) ([]model.AuditEntry, error) {
	var out []model.AuditEntry
	for i := len(m.entries) - 1; i >= 0 && len(out) < f.Limit; i-- {
		e := m.entries[i]
		if (f.Before != 0 && e.ID >= f.Before) || (f.ObjectType != "" && e.ObjectType != f.ObjectType) ||
			(!f.From.IsZero() && e.At.Before(f.From)) || (!f.To.IsZero() && !e.At.Before(f.To)) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func auditServer(n int) (*Server, *auditMem) {
	_, base, _ := newServer()
	st := &auditMem{memStore: base}
	day := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= n; i++ {
		st.entries = append(st.entries, model.AuditEntry{ID: int64(i), At: day.Add(time.Duration(i) * time.Hour), Actor: "a@x.io",
			Action: "PUT /v1/segments/{id}", ObjectType: "segments", ObjectID: "1", Status: 200, IP: "10.0.0.1"})
	}
	return New(Deps{Store: st, Pub: &memPub{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}), st
}

func TestAuditExportCSVPagesAndDefusesFormulas(t *testing.T) {
	s, st := auditServer(2500) // three pages of 1,000
	st.entries[0].Actor = "=HYPERLINK(\"http://evil\")"
	st.entries[0].Detail = json.RawMessage(`{"name":"x"}`)
	w := do(t, s.Handler(), "GET", "/v1/audit/export", "key-1", "")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("status %d, type %q: %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Error("export should download as a file")
	}
	rows, err := csv.NewReader(w.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2501 || rows[0][0] != "id" {
		t.Fatalf("got %d rows, header %v", len(rows), rows[0])
	}
	last := rows[len(rows)-1]
	if last[0] != "1" || last[3] != `'=HYPERLINK("http://evil")` {
		t.Errorf("oldest row %v: the actor must be defused", last)
	}
}

func TestAuditExportJSONWithDateRange(t *testing.T) {
	s, _ := auditServer(48) // 2026-11-01 13:00 .. 2026-11-03 12:00
	w := do(t, s.Handler(), "GET", "/v1/audit/export?format=json&from=2026-11-02&to=2026-11-02&object_type=segments", "key-1", "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got []model.AuditEntry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, w.Body.String())
	}
	if len(got) != 24 {
		t.Fatalf("2 November only: got %d entries", len(got))
	}
	for _, e := range got {
		if e.At.Format("2006-01-02") != "2026-11-02" {
			t.Errorf("entry %d at %s is outside the range", e.ID, e.At)
		}
	}
	// An empty result is still a valid array.
	w = do(t, s.Handler(), "GET", "/v1/audit/export?format=json&object_type=forms", "key-1", "")
	var none []any
	if json.Unmarshal(w.Body.Bytes(), &none) != nil || len(none) != 0 {
		t.Errorf("empty export: %q", w.Body.String())
	}
}

func TestAuditExportValidation(t *testing.T) {
	s, _ := auditServer(1)
	for _, q := range []string{"format=xml", "from=yesterday", "from=2026-11-03&to=2026-11-01"} {
		if w := do(t, s.Handler(), "GET", "/v1/audit/export?"+q, "key-1", ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", q, w.Code)
		}
	}
}
