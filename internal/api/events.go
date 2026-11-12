package api

// Data → Events: the catalog of events a workspace sends (volume from ClickHouse), each event's
// discovered properties, and event schemas with their checks (see internal/eventschema).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/eventschema"
	"pulse/internal/model"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

type EventSchemaStore interface {
	ListEventSchemas(ctx context.Context, tenant int64) ([]eventschema.Schema, error)
	GetEventSchema(ctx context.Context, tenant int64, name string) (eventschema.Schema, error)
	PutEventSchema(ctx context.Context, tenant int64, s eventschema.Schema) error
	DeleteEventSchema(ctx context.Context, tenant int64, name string) error
}

// EventCatalog reads event volumes and samples from ClickHouse.
type EventCatalog interface {
	EventCatalog(ctx context.Context, tenant int64, since time.Time) ([]ch.CatalogRow, error)
	EventVolume(ctx context.Context, tenant int64, name string, since time.Time) ([]ch.DayCount, error)
	EventSamples(ctx context.Context, tenant int64, name string, since time.Time, limit int) ([]string, error)
}

const (
	catalogDays    = 30
	catalogTTL     = 2 * time.Minute
	problemDays    = 7 // the catalog's problem counts
	summaryDays    = 14
	discoverSample = 500
	enforceConfirm = "ENFORCE"
)

func (s *Server) eventSchemas() (EventSchemaStore, bool) {
	st, ok := s.Store.(EventSchemaStore)
	return st, ok
}

func (s *Server) eventRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/events/catalog", s.tenant(s.eventCatalog))
	mux.HandleFunc("GET /v1/events/schemas", s.tenant(s.listEventSchemas))
	mux.HandleFunc("GET /v1/events/schemas/suggested", s.tenant(s.suggestedSchemas))
	mux.HandleFunc("POST /v1/events/schemas/suggested", s.authed(s.addSuggestedSchemas))
	mux.HandleFunc("GET /v1/events/{name}/summary", s.tenant(s.eventSummary))
	mux.HandleFunc("GET /v1/events/{name}/schema", s.tenant(s.getEventSchema))
	mux.HandleFunc("PUT /v1/events/{name}/schema", s.authed(s.putEventSchema))
	mux.HandleFunc("DELETE /v1/events/{name}/schema", s.authed(s.deleteEventSchema))
}

// catalogEntry is one row of the Events page: an event sent in the last 30 days, one with a
// schema, or both.
type catalogEntry struct {
	Name     string     `json:"name"`
	Label    string     `json:"label"`
	Count    uint64     `json:"count"`    // last 30 days
	Contacts uint64     `json:"contacts"` // approximate
	LastSeen *time.Time `json:"last_seen,omitempty"`
	Schema   bool       `json:"schema"`
	Mode     string     `json:"mode,omitempty"`
	Props    int        `json:"props"`
	Problems int64      `json:"problems"` // events with problems in the last 7 days (kept or refused)
	Checked  int64      `json:"checked"`
}

// eventCatalog: GET /v1/events/catalog.
func (s *Server) eventCatalog(w http.ResponseWriter, r *http.Request, tenant int64) {
	ctx := r.Context()
	rows, err := s.catalogRows(ctx, tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	byName := map[string]*catalogEntry{}
	var out []*catalogEntry
	for _, row := range rows {
		e := &catalogEntry{Name: row.Name, Count: row.Count, Contacts: row.Contacts}
		if !row.LastSeen.IsZero() {
			t := row.LastSeen
			e.LastSeen = &t
		}
		byName[row.Name] = e
		out = append(out, e)
	}
	if st, ok := s.eventSchemas(); ok {
		schemas, err := st.ListEventSchemas(ctx, tenant)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, sc := range schemas {
			e := byName[sc.Name]
			if e == nil {
				e = &catalogEntry{Name: sc.Name}
				byName[sc.Name] = e
				out = append(out, e)
			}
			e.Schema, e.Mode, e.Label, e.Props = true, sc.Mode, sc.Label, len(sc.Props)
		}
	}
	if s.RDB != nil {
		names := make([]string, 0, len(out))
		for _, e := range out {
			if e.Schema {
				names = append(names, e.Name)
			}
		}
		totals, err := eventschema.Totals(ctx, s.RDB, tenant, names, problemDays, s.now())
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, e := range out {
			t := totals[e.Name]
			e.Problems, e.Checked = t[0], t[1]
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if out == nil {
		out = []*catalogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out, "days": catalogDays})
}

// catalogRows reads the 30-day volumes, cached briefly in Redis so the page doesn't rescan.
func (s *Server) catalogRows(ctx context.Context, tenant int64) ([]ch.CatalogRow, error) {
	cat, ok := s.Analytics.(EventCatalog)
	if !ok {
		return nil, nil
	}
	key := fmt.Sprintf("evcat:%d", tenant)
	if s.RDB != nil {
		if b, err := s.RDB.Get(ctx, key).Bytes(); err == nil {
			var rows []ch.CatalogRow
			if json.Unmarshal(b, &rows) == nil {
				return rows, nil
			}
		}
	}
	// Whole UTC days, today included, so "last 30 days" is the same window the contact list's
	// period: filter opens from a count (M7).
	rows, err := cat.EventCatalog(ctx, tenant, s.now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -(catalogDays-1)))
	if err != nil {
		return nil, err
	}
	if s.RDB != nil {
		b, _ := json.Marshal(rows)
		s.RDB.Set(ctx, key, b, catalogTTL)
	}
	return rows, nil
}

// eventSummary: GET /v1/events/{name}/summary, the event's daily volume, the properties seen in
// a sample of its events, its schema and its checks.
func (s *Server) eventSummary(w http.ResponseWriter, r *http.Request, tenant int64) {
	ctx := r.Context()
	name := r.PathValue("name")
	if !event.ValidName(name) {
		writeErr(w, http.StatusBadRequest, "invalid event name")
		return
	}
	out := map[string]any{"name": name, "days": catalogDays, "volume": []ch.DayCount{}, "properties": []eventschema.Discovered{}}
	var schema *eventschema.Schema
	if st, ok := s.eventSchemas(); ok {
		sc, err := st.GetEventSchema(ctx, tenant, name)
		switch {
		case err == nil:
			_ = sc.Normalize()
			schema = &sc
			out["schema"] = sc
		case !errors.Is(err, pg.ErrNotFound):
			s.fail(w, err)
			return
		}
	}
	if cat, ok := s.Analytics.(EventCatalog); ok {
		since := s.now().AddDate(0, 0, -catalogDays)
		vol, err := cat.EventVolume(ctx, tenant, name, since)
		if err != nil {
			s.fail(w, err)
			return
		}
		out["volume"] = vol
		raws, err := cat.EventSamples(ctx, tenant, name, since, discoverSample)
		if err != nil {
			s.fail(w, err)
			return
		}
		samples := make([]map[string]any, 0, len(raws))
		for _, raw := range raws {
			var p map[string]any
			if json.Unmarshal([]byte(raw), &p) == nil {
				samples = append(samples, p)
			}
		}
		personal := map[string]bool{}
		if schema != nil {
			for _, p := range schema.PersonalPaths() {
				personal[p] = true
			}
		}
		out["properties"] = eventschema.Discover(samples, func(path string) bool {
			last := path[strings.LastIndexAny(path, ".]")+1:]
			return personal[path] || personalKey(strings.TrimPrefix(last, "."))
		})
		out["sampled"] = len(samples)
	}
	if s.RDB != nil {
		sum, err := eventschema.Summarize(ctx, s.RDB, tenant, name, summaryDays, s.now())
		if err != nil {
			s.fail(w, err)
			return
		}
		out["checks"] = sum
	}
	writeJSON(w, http.StatusOK, out)
}

// personalKey reports whether a property name looks like personal data, whose example values
// are never shown.
func personalKey(k string) bool {
	switch strings.ToLower(k) {
	case "email", "phone", "mobile", "first_name", "last_name", "name", "full_name", "address", "zip", "postal_code",
		"user_id", "external_id", "password", "token", "otp", "ip":
		return true
	}
	return false
}

func (s *Server) listEventSchemas(w http.ResponseWriter, r *http.Request, tenant int64) {
	st, ok := s.eventSchemas()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"schemas": []eventschema.Schema{}})
		return
	}
	list, err := st.ListEventSchemas(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []eventschema.Schema{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schemas": list})
}

func (s *Server) getEventSchema(w http.ResponseWriter, r *http.Request, tenant int64) {
	st, ok := s.eventSchemas()
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	sc, err := st.GetEventSchema(r.Context(), tenant, r.PathValue("name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

// putEventSchema: PUT /v1/events/{name}/schema. Switching a schema to enforce (the server API then
// refuses events with problems) needs settings edit access and "confirm": "ENFORCE".
func (s *Server) putEventSchema(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	st, ok := s.eventSchemas()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "event schemas aren't available")
		return
	}
	var req struct {
		eventschema.Schema
		Confirm string `json:"confirm"`
	}
	if !decode(w, r, &req) {
		return
	}
	sc := req.Schema
	sc.Name = r.PathValue("name")
	if err := sc.Normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	old, err := st.GetEventSchema(ctx, tenant, sc.Name)
	if err != nil && !errors.Is(err, pg.ErrNotFound) {
		s.fail(w, err)
		return
	}
	// An enforced schema decides which events the API accepts, so any change to one (its
	// properties, or turning enforce on or off) needs settings edit.
	if (sc.Mode == eventschema.ModeEnforce || old.Mode == eventschema.ModeEnforce) && !p.Perms.Allows(auth.AreaSettings, auth.Edit) {
		writeErr(w, http.StatusForbidden, "changing an enforced schema needs edit access to settings: it changes which events the API accepts")
		return
	}
	if sc.Mode == eventschema.ModeEnforce && old.Mode != eventschema.ModeEnforce {
		if req.Confirm != enforceConfirm {
			writeErr(w, http.StatusBadRequest, `to refuse events that don't fit, send "confirm": "ENFORCE"`)
			return
		}
	}
	if err := s.ensureListFields(ctx, p, sc); err != nil {
		var bad badRequest
		if errors.As(err, &bad) {
			writeErr(w, bad.status(http.StatusBadRequest), err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	if err := st.PutEventSchema(ctx, tenant, sc); err != nil {
		s.fail(w, err)
		return
	}
	s.bumpEventSchemas(ctx)
	sc.UpdatedAt = s.now()
	writeJSON(w, http.StatusOK, sc)
}

type badRequest string

func (b badRequest) Error() string { return string(b) }

// errNeedsFieldAccess: a schema would create a contact field, which needs segments edit.
const errNeedsFieldAccess badRequest = "adding values to a new list field creates that contact field, which needs edit access to segments; create the field first in Settings → Contact fields"

func (b badRequest) status(def int) int {
	if b == errNeedsFieldAccess {
		return http.StatusForbidden
	}
	return def
}

// ensureListFields creates the list contact fields a schema adds values to, and refuses a field
// that exists with another type.
// Creating a field needs segments edit (like Settings → Contact fields).
func (s *Server) ensureListFields(ctx context.Context, p model.Principal, sc eventschema.Schema) error {
	tenant := p.TenantID
	as, ok := s.Store.(AudienceStore)
	if !ok {
		return nil
	}
	var defs []pg.AttributeDef
	loaded := false
	for _, prop := range sc.Props {
		if prop.AddToList == "" {
			continue
		}
		if !loaded {
			var err error
			if defs, err = as.AttributeDefs(ctx, tenant); err != nil {
				return err
			}
			loaded = true
		}
		found := false
		for _, d := range defs {
			if d.Key == prop.AddToList {
				found = true
				if d.Type != "list" {
					return badRequest(fmt.Sprintf("property %q adds to %q, which is a %s field; choose a list field", prop.Key, d.Key, d.Type))
				}
			}
		}
		if !found {
			if !p.Perms.Allows(auth.AreaSegments, auth.Edit) {
				return errNeedsFieldAccess
			}
			d := pg.AttributeDef{Key: prop.AddToList, Label: labelFor(prop.AddToList), Type: "list",
				Description: "Values added by the " + sc.Name + " event"}
			if err := as.PutAttributeDef(ctx, tenant, d); err != nil {
				return err
			}
			defs = append(defs, d)
		}
	}
	return nil
}

// labelFor turns a key into words: products → Products, product_codes → Product codes.
func labelFor(key string) string {
	s := strings.ReplaceAll(key, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (s *Server) deleteEventSchema(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	st, ok := s.eventSchemas()
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	old, err := st.GetEventSchema(r.Context(), tenant, r.PathValue("name"))
	if err != nil && !errors.Is(err, pg.ErrNotFound) {
		s.fail(w, err)
		return
	}
	if old.Mode == eventschema.ModeEnforce && !p.Perms.Allows(auth.AreaSettings, auth.Edit) {
		writeErr(w, http.StatusForbidden, "deleting an enforced schema needs edit access to settings: it changes which events the API accepts")
		return
	}
	if err := st.DeleteEventSchema(r.Context(), tenant, r.PathValue("name")); err != nil {
		s.fail(w, err)
		return
	}
	s.bumpEventSchemas(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// suggestedSchemas: GET /v1/events/schemas/suggested, the starter schemas and which exist.
func (s *Server) suggestedSchemas(w http.ResponseWriter, r *http.Request, tenant int64) {
	existing := map[string]bool{}
	if st, ok := s.eventSchemas(); ok {
		list, err := st.ListEventSchemas(r.Context(), tenant)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, sc := range list {
			existing[sc.Name] = true
		}
	}
	type item struct {
		eventschema.Schema
		Exists bool `json:"exists"`
	}
	var out []item
	for _, sc := range eventschema.Suggested() {
		out = append(out, item{sc, existing[sc.Name]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"schemas": out})
}

// addSuggestedSchemas: POST /v1/events/schemas/suggested {"names": [...]} (all when empty) adds
// the starter schemas the workspace doesn't have yet; existing schemas are never replaced.
func (s *Server) addSuggestedSchemas(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	st, ok := s.eventSchemas()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "event schemas aren't available")
		return
	}
	var req struct {
		Names []string `json:"names"`
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	list, err := st.ListEventSchemas(ctx, tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	have := map[string]bool{}
	for _, sc := range list {
		have[sc.Name] = true
	}
	want := map[string]bool{}
	for _, n := range req.Names {
		want[n] = true
	}
	added := []string{}
	for _, sc := range eventschema.Suggested() {
		if have[sc.Name] || (len(want) > 0 && !want[sc.Name]) {
			continue
		}
		if err := sc.Normalize(); err != nil {
			s.fail(w, err)
			return
		}
		if err := s.ensureListFields(ctx, p, sc); err != nil {
			var bad badRequest
			if errors.As(err, &bad) {
				writeErr(w, bad.status(http.StatusConflict), sc.Name+": "+err.Error())
				return
			}
			s.fail(w, err)
			return
		}
		if err := st.PutEventSchema(ctx, tenant, sc); err != nil {
			s.fail(w, err)
			return
		}
		added = append(added, sc.Name)
	}
	if len(added) > 0 {
		s.bumpEventSchemas(ctx)
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added})
}

// bumpEventSchemas tells the profile and API roles to reload schemas (eventschema.VersionKey).
func (s *Server) bumpEventSchemas(ctx context.Context) {
	if s.RDB != nil {
		if err := eventschema.Bump(ctx, s.RDB); err != nil {
			s.Log.Warn("bump event schema version", "err", err)
		}
	}
	s.Schemas.Invalidate()
}

// enforceSchemas checks events sent to POST /v1/events against schemas in enforce mode. It
// returns the first refused event's message ("" when all fit), after counting the refusal.
func (s *Server) enforceSchemas(ctx context.Context, tenant int64, evs []event.Event) string {
	schemas := s.Schemas.Tenant(ctx, tenant)
	if len(schemas) == 0 {
		return ""
	}
	for i := range evs {
		e := &evs[i]
		sc := schemas[e.Name]
		if sc == nil || sc.Mode != eventschema.ModeEnforce {
			continue
		}
		vs := sc.Check(e.Props)
		if len(vs) == 0 {
			continue
		}
		if s.RDB != nil {
			if err := eventschema.Refused(ctx, s.RDB, sc, e, vs, s.now()); err != nil {
				s.Log.Warn("count refused event", "err", err)
			}
		}
		msgs := make([]string, len(vs))
		for j, v := range vs {
			msgs[j] = v.Message
		}
		return "event " + strconv.Itoa(i) + ": " + e.Name + " doesn't fit its schema: " + strings.Join(msgs, "; ")
	}
	return ""
}
