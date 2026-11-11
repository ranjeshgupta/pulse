package api

// Personalisation helpers for the template editor: sample trigger events for previews and test
// sends ({{event.x}}).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"pulse/internal/event"
	"pulse/internal/eventschema"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

// LatestEvents finds a contact's latest event of a name (ClickHouse).
type LatestEvents interface {
	LatestEvent(ctx context.Context, tenant int64, contact uint32, name string) (ch.EventRow, bool, error)
}

func (s *Server) personalizeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/templates/sample-event", s.tenant(s.sampleEvent))
}

// eventFits refuses sample event properties larger than a journey would keep (16 KB).
func eventFits(w http.ResponseWriter, ev map[string]any) bool {
	if ev == nil {
		return true
	}
	if b, _ := json.Marshal(ev); len(b) > eventschema.MaxMessageProps {
		writeErr(w, http.StatusBadRequest, "the sample event is larger than 16 KB")
		return false
	}
	return true
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// sampleEvent returns properties to preview {{event.x}} with: the contact's latest real event of
// that name (personal properties removed, as in messages), else an example built from the event's
// schema. source says which: latest | schema | none.
func (s *Server) sampleEvent(w http.ResponseWriter, r *http.Request, tenant int64) {
	name, ext := r.URL.Query().Get("event"), r.URL.Query().Get("external_id")
	if !event.ValidName(name) {
		writeErr(w, http.StatusBadRequest, "event: the name of an event, e.g. cart_abandoned")
		return
	}
	var schema *eventschema.Schema
	if st, ok := s.eventSchemas(); ok {
		sc, err := st.GetEventSchema(r.Context(), tenant, name)
		switch {
		case err == nil:
			schema = &sc
		case !errors.Is(err, pg.ErrNotFound):
			s.fail(w, err)
			return
		}
	}
	if ext != "" {
		c, err := s.Store.ContactByExternalID(r.Context(), tenant, ext)
		if err != nil {
			s.fail(w, err)
			return
		}
		if le, ok := s.Analytics.(LatestEvents); ok {
			row, found, err := le.LatestEvent(r.Context(), tenant, c.ID, name)
			if err != nil {
				s.fail(w, err)
				return
			}
			if found {
				var props map[string]any
				_ = json.Unmarshal([]byte(row.Props), &props)
				kept := map[string]any{}
				_ = json.Unmarshal(eventschema.MessageProps(schema, props), &kept)
				writeJSON(w, http.StatusOK, map[string]any{"source": "latest", "at": row.Ts.UTC().Format(time.RFC3339), "props": kept})
				return
			}
		}
	}
	if schema != nil {
		writeJSON(w, http.StatusOK, map[string]any{"source": "schema", "props": eventschema.Example(schema)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": "none", "props": map[string]any{}})
}
