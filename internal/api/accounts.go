package api

// Lifecycle stages, companies (B2B accounts) and duplicate merges.
//
// Like tags and lists, a contact's stage, company and merges change only through events
// (stage_changed, company_linked / company_updated / company_unlinked, merge_requested) that the
// profile role applies in order with the contact's other events. These handlers validate a change
// against the workspace's definitions and publish the event; segments and journeys react at once.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"pulse/internal/attr"
	"pulse/internal/event"
	"pulse/internal/journey"
	"pulse/internal/model"
	"pulse/internal/profile"
	"pulse/internal/store/pg"
)

const (
	maxCompanyName  = 200
	maxCompanyAttrs = 50
	maxCompanyValue = 500
	// A company attribute key becomes company_<key>, which must stay a valid (64-character) key.
	maxCompanyKey   = 64 - len(event.CompanyAttrPrefix)
	maxStages       = 50
	maxStageDesc    = 500
	maxMinPoints    = 1_000_000_000
	fanOutPage      = 2000
	maxDupGroups    = 200
	dupPerGroup     = 20
	companyMembers  = 200
	mergeLockTTL    = 15 * time.Second
	fanOutTimeLimit = 30 * time.Minute
)

type AccountStore interface {
	ListStages(ctx context.Context, tenant int64) ([]pg.Stage, error)
	StageCounts(ctx context.Context, tenant int64) (map[string]int64, error)
	CreateStage(ctx context.Context, tenant int64, st pg.Stage) (int64, error)
	UpdateStage(ctx context.Context, tenant int64, st pg.Stage) (string, error)
	DeleteStage(ctx context.Context, tenant, id int64) error
	ReorderStages(ctx context.Context, tenant int64, ids []int64) error

	ListCompanies(ctx context.Context, tenant int64, q string, offset, limit int) ([]pg.Company, error)
	GetCompany(ctx context.Context, tenant, id int64) (pg.Company, error)
	CreateCompany(ctx context.Context, tenant int64, c pg.Company) (int64, error)
	UpdateCompany(ctx context.Context, tenant int64, c pg.Company) error
	DeleteCompany(ctx context.Context, tenant, id int64) error
	UpsertCompanyByName(ctx context.Context, tenant int64, name string) (pg.Company, error)
	CompanyMembers(ctx context.Context, tenant, id int64, limit int) ([]model.Contact, error)
	MemberIDs(ctx context.Context, tenant int64, key, value string, after uint32, limit int) ([]string, uint32, error)

	DuplicateContacts(ctx context.Context, tenant int64, by string, limit, perGroup int) ([]pg.DuplicateGroup, error)
	ListSegmentsOfKind(ctx context.Context, tenant int64, kind string) ([]model.Segment, error)
}

var _ AccountStore = (*pg.Store)(nil)

func (s *Server) accounts() AccountStore { return s.Store.(AccountStore) }

func (s *Server) accountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/stages", s.tenant(s.listStages))
	mux.HandleFunc("POST /v1/stages", s.tenant(s.createStage))
	mux.HandleFunc("PUT /v1/stages/order", s.tenant(s.reorderStages))
	mux.HandleFunc("PUT /v1/stages/{id}", s.tenant(s.updateStage))
	mux.HandleFunc("DELETE /v1/stages/{id}", s.tenant(s.deleteStage))
	mux.HandleFunc("PUT /v1/contacts/{external_id}/stage", s.tenant(s.contactStage))
	mux.HandleFunc("PUT /v1/contacts/{external_id}/owner", s.tenant(s.contactOwner))

	mux.HandleFunc("GET /v1/companies", s.tenant(s.listCompanies))
	mux.HandleFunc("POST /v1/companies", s.tenant(s.createCompany))
	mux.HandleFunc("GET /v1/companies/{id}", s.tenant(s.getCompany))
	mux.HandleFunc("PUT /v1/companies/{id}", s.tenant(s.updateCompany))
	mux.HandleFunc("DELETE /v1/companies/{id}", s.tenant(s.deleteCompany))
	mux.HandleFunc("PUT /v1/contacts/{external_id}/company", s.tenant(s.contactCompany))

	mux.HandleFunc("POST /v1/contacts/merge", s.tenant(s.mergeContacts))
	mux.HandleFunc("GET /v1/contacts/duplicates", s.tenant(s.duplicateContacts))
}

// ---- stages ----

// stageReq is the body of stage create and update.
type stageReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MinPoints   *int   `json:"min_points"`
}

func (req *stageReq) valid(w http.ResponseWriter) bool {
	req.Name, req.Description = strings.TrimSpace(req.Name), strings.TrimSpace(req.Description)
	switch {
	case !profile.ValidStageName(req.Name):
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("name: 1-%d characters", profile.MaxStageName))
	case len(req.Description) > maxStageDesc:
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("keep the description under %d characters", maxStageDesc))
	case req.MinPoints != nil && (*req.MinPoints < 0 || *req.MinPoints > maxMinPoints):
		writeErr(w, http.StatusBadRequest, "min_points: a whole number from 0, or null for no threshold")
	default:
		return true
	}
	return false
}

func (s *Server) listStages(w http.ResponseWriter, r *http.Request, tenant int64) {
	stages, err := s.accounts().ListStages(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	counts, err := s.accounts().StageCounts(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range stages {
		stages[i].Contacts = counts[stages[i].Name]
	}
	if stages == nil {
		stages = []pg.Stage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"stages": stages})
}

func (s *Server) createStage(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req stageReq
	if !decode(w, r, &req) || !req.valid(w) {
		return
	}
	existing, err := s.accounts().ListStages(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(existing) >= maxStages {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("a workspace can have up to %d stages", maxStages))
		return
	}
	id, err := s.accounts().CreateStage(r.Context(), tenant, pg.Stage{Name: req.Name, Description: req.Description, MinPoints: req.MinPoints})
	if errors.Is(err, pg.ErrConflict) {
		writeErr(w, http.StatusConflict, fmt.Sprintf("there is already a stage called %q", req.Name))
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.bumpScoring(r.Context())
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// updateStage changes a stage. Renaming it renames it on every contact in it too.
func (s *Server) updateStage(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req stageReq
	if !decode(w, r, &req) || !req.valid(w) {
		return
	}
	old, err := s.accounts().UpdateStage(r.Context(), tenant, pg.Stage{ID: id, Name: req.Name, Description: req.Description, MinPoints: req.MinPoints})
	if errors.Is(err, pg.ErrConflict) {
		writeErr(w, http.StatusConflict, fmt.Sprintf("there is already a stage called %q", req.Name))
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.bumpScoring(r.Context())
	if old != req.Name {
		s.fanOut(tenant, "stage", old, func(ext string) event.Event {
			return event.Event{ExternalID: ext, Name: event.StageChanged,
				Props: map[string]any{"stage": req.Name, "from": old, "rename": true, "source": "rename"}}
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// deleteStage removes a stage definition. Contacts in it keep the name (segments on it still
// work) until they move to another stage.
func (s *Server) deleteStage(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.accounts().DeleteStage(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	s.bumpScoring(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reorderStages(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !decode(w, r, &req) {
		return
	}
	seen := map[int64]bool{}
	for _, id := range req.IDs {
		if seen[id] {
			writeErr(w, http.StatusBadRequest, "list each stage once")
			return
		}
		seen[id] = true
	}
	if err := s.accounts().ReorderStages(r.Context(), tenant, req.IDs); err != nil {
		if errors.Is(err, pg.ErrInvalid) {
			writeErr(w, http.StatusBadRequest, "send the ids of all the workspace's stages, in their new order")
			return
		}
		s.fail(w, err)
		return
	}
	s.bumpScoring(r.Context())
	s.listStages(w, r, tenant)
}

// stageNamed returns the workspace's stage called name, ignoring letter case.
func (s *Server) stageNamed(ctx context.Context, tenant int64, name string) (string, bool, error) {
	return s.stageLookup(tenant)(ctx, name)
}

// stageLookup is stageNamed for many names, loading the workspace's stages once.
func (s *Server) stageLookup(tenant int64) func(ctx context.Context, name string) (string, bool, error) {
	var stages []pg.Stage
	loaded := false
	return func(ctx context.Context, name string) (string, bool, error) {
		if !loaded {
			var err error
			if stages, err = s.accounts().ListStages(ctx, tenant); err != nil {
				return "", false, err
			}
			loaded = true
		}
		for _, st := range stages {
			if strings.EqualFold(st.Name, strings.TrimSpace(name)) {
				return st.Name, true, nil
			}
		}
		return "", false, nil
	}
}

// knownContact loads a contact for a change and refuses one that was merged into another.
func (s *Server) knownContact(w http.ResponseWriter, r *http.Request, tenant int64, ext string) (model.Contact, bool) {
	c, err := s.Store.ContactByExternalID(r.Context(), tenant, ext)
	if err != nil {
		s.fail(w, err)
		return c, false
	}
	if into := attr.String(c.Attrs[event.AttrMergedInto]); into != "" {
		writeErr(w, http.StatusConflict, fmt.Sprintf("%s was merged into %s; change that contact instead", ext, into))
		return c, false
	}
	return c, true
}

func (s *Server) contactStage(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Stage string `json:"stage"`
	}
	if !decode(w, r, &req) {
		return
	}
	name, ok, err := s.stageNamed(r.Context(), tenant, req.Stage)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("there is no stage called %q", req.Stage))
		return
	}
	c, ok := s.knownContact(w, r, tenant, r.PathValue("external_id"))
	if !ok {
		return
	}
	ev := event.Event{ExternalID: c.ExternalID, Name: event.StageChanged, Props: map[string]any{"stage": name, "source": "manual"}}
	if err := s.publishEvents(r.Context(), tenant, []event.Event{ev}); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "event bus unavailable, retry")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"stage": name})
}

// contactOwner assigns the contact to a user of the workspace ({"user_id": 7}), or unassigns it
// ({"user_id": 0}). It records owner_changed, which sets owner_id and owner_name.
func (s *Server) contactOwner(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	props := map[string]any{"owner_id": 0, "source": "manual"}
	var name string
	if req.UserID != 0 {
		users, err := s.Store.ListUsers(r.Context(), tenant)
		if err != nil {
			s.fail(w, err)
			return
		}
		found := false
		for _, u := range users {
			if u.ID == req.UserID {
				found, name = true, u.Name
				if name == "" {
					name = u.Email
				}
			}
		}
		if !found {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("there is no user %d in this workspace", req.UserID))
			return
		}
		props["owner_id"], props["owner_name"] = req.UserID, name
	}
	c, ok := s.knownContact(w, r, tenant, r.PathValue("external_id"))
	if !ok {
		return
	}
	ev := event.Event{ExternalID: c.ExternalID, Name: event.OwnerChanged, Props: props}
	if err := s.publishEvents(r.Context(), tenant, []event.Event{ev}); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "event bus unavailable, retry")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"owner_id": req.UserID, "owner_name": name})
}

// checkJourneyStages makes sure every change_stage step of a journey names a stage of the workspace.
func (s *Server) checkJourneyStages(ctx context.Context, tenant int64, graph json.RawMessage) error {
	g, err := journey.ParseGraph(graph)
	if err != nil {
		return err
	}
	var want []string
	for id, n := range g.Nodes {
		if n.Type == journey.NodeStage {
			want = append(want, id)
		}
	}
	if len(want) == 0 {
		return nil
	}
	stages, err := s.accounts().ListStages(ctx, tenant)
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, st := range stages {
		names[st.Name] = true
	}
	for _, id := range want {
		if st := g.Nodes[id].Stage; !names[st] {
			return fmt.Errorf("%w: step %q: there is no stage called %q", errBadJourney, id, st)
		}
	}
	return nil
}

var errBadJourney = errors.New("journey")

// ---- companies ----

type companyReq struct {
	Name   string         `json:"name"`
	Domain string         `json:"domain"`
	Attrs  map[string]any `json:"attrs"`
}

func (req *companyReq) valid(w http.ResponseWriter) bool {
	req.Name, req.Domain = strings.TrimSpace(req.Name), strings.ToLower(strings.TrimSpace(req.Domain))
	if req.Attrs == nil {
		req.Attrs = map[string]any{}
	}
	if err := validCompany(req.Name, req.Domain, req.Attrs); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func validCompany(name, domain string, attrs map[string]any) error {
	switch {
	case name == "" || len(name) > maxCompanyName:
		return fmt.Errorf("name: 1-%d characters", maxCompanyName)
	case len(domain) > 253 || strings.ContainsAny(domain, " /@:"):
		return errors.New("domain: a domain name like example.com")
	case len(attrs) > maxCompanyAttrs:
		return fmt.Errorf("a company can have up to %d attributes", maxCompanyAttrs)
	}
	for k, v := range attrs {
		if !attr.ValidKey(k) || len(k) > maxCompanyKey || k == "id" || k == "name" {
			return fmt.Errorf("attribute %q: letters, digits and _ (up to %d), not id or name", k, maxCompanyKey)
		}
		switch x := v.(type) {
		case string:
			if len(x) > maxCompanyValue {
				return fmt.Errorf("attribute %q: keep it under %d characters", k, maxCompanyValue)
			}
		case float64, bool:
		default:
			return fmt.Errorf("attribute %q: use text, a number or true/false", k)
		}
	}
	return nil
}

// companyProps are the props of a company_linked or company_updated event: what the contact copies.
func companyProps(c pg.Company, source string) map[string]any {
	return map[string]any{"company_id": c.ID, "name": c.Name, "attrs": c.Attrs, "source": source}
}

func (s *Server) listCompanies(w http.ResponseWriter, r *http.Request, tenant int64) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	cs, err := s.accounts().ListCompanies(r.Context(), tenant, q.Get("q"), offset, limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	if cs == nil {
		cs = []pg.Company{}
	}
	next := ""
	if len(cs) == limit {
		next = strconv.Itoa(offset + limit)
	}
	writeJSON(w, http.StatusOK, map[string]any{"companies": cs, "next": next})
}

func (s *Server) getCompany(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := s.accounts().GetCompany(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	members, err := s.accounts().CompanyMembers(r.Context(), tenant, id, companyMembers)
	if err != nil {
		s.fail(w, err)
		return
	}
	if members == nil {
		members = []model.Contact{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"company": c, "members": members})
}

func (s *Server) createCompany(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req companyReq
	if !decode(w, r, &req) || !req.valid(w) {
		return
	}
	id, err := s.accounts().CreateCompany(r.Context(), tenant, pg.Company{Name: req.Name, Domain: req.Domain, Attrs: req.Attrs})
	if errors.Is(err, pg.ErrConflict) {
		writeErr(w, http.StatusConflict, fmt.Sprintf("there is already a company called %q", req.Name))
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// updateCompany saves a company and refreshes the copies of its name and attributes on every
// linked contact, in the background.
func (s *Server) updateCompany(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req companyReq
	if !decode(w, r, &req) || !req.valid(w) {
		return
	}
	c := pg.Company{ID: id, Name: req.Name, Domain: req.Domain, Attrs: req.Attrs}
	err := s.accounts().UpdateCompany(r.Context(), tenant, c)
	if errors.Is(err, pg.ErrConflict) {
		writeErr(w, http.StatusConflict, fmt.Sprintf("there is already a company called %q", req.Name))
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.fanOut(tenant, event.AttrCompanyID, strconv.FormatInt(id, 10), func(ext string) event.Event {
		return event.Event{ExternalID: ext, Name: event.CompanyUpdated, Props: companyProps(c, "company_updated")}
	})
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// deleteCompany removes a company and unlinks its contacts (in the background).
func (s *Server) deleteCompany(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.accounts().DeleteCompany(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	s.fanOut(tenant, event.AttrCompanyID, strconv.FormatInt(id, 10), func(ext string) event.Event {
		return event.Event{ExternalID: ext, Name: event.CompanyUnlinked, Props: map[string]any{"company_id": id, "source": "company_deleted"}}
	})
	w.WriteHeader(http.StatusNoContent)
}

// contactCompany links a contact to a company ({"company_id": 12}) or unlinks it ({"company_id": null}).
func (s *Server) contactCompany(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		CompanyID json.RawMessage `json:"company_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	var id *int64
	if len(req.CompanyID) == 0 || json.Unmarshal(req.CompanyID, &id) != nil || id != nil && *id <= 0 {
		writeErr(w, http.StatusBadRequest, "send company_id: a company's id, or null to unlink")
		return
	}
	c, ok := s.knownContact(w, r, tenant, r.PathValue("external_id"))
	if !ok {
		return
	}
	ev := event.Event{ExternalID: c.ExternalID, Name: event.CompanyUnlinked, Props: map[string]any{"source": "manual"}}
	var company *pg.Company
	if id != nil {
		co, err := s.accounts().GetCompany(r.Context(), tenant, *id)
		if errors.Is(err, pg.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "that company doesn't exist")
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		company = &co
		ev = event.Event{ExternalID: c.ExternalID, Name: event.CompanyLinked, Props: companyProps(co, "manual")}
	}
	if err := s.publishEvents(r.Context(), tenant, []event.Event{ev}); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "event bus unavailable, retry")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"company": company})
}

// linkCompanies follows each identify that carries attrs.company (a company name) with a
// company_linked event, creating the company when the workspace has none by that name (ignoring
// letter case). Server API ingest only: a browser can't create companies.
func (s *Server) linkCompanies(ctx context.Context, tenant int64, evs []event.Event) ([]event.Event, error) {
	var out []event.Event
	byName := map[string]pg.Company{}
	for i := range evs {
		e := &evs[i]
		out = append(out, *e)
		if e.Name != event.Identify {
			continue
		}
		name, _ := e.Attrs["company"].(string)
		if name = strings.TrimSpace(name); name == "" || len(name) > maxCompanyName {
			continue
		}
		key := strings.ToLower(name)
		c, ok := byName[key]
		if !ok {
			var err error
			if c, err = s.accounts().UpsertCompanyByName(ctx, tenant, name); err != nil {
				return nil, err
			}
			byName[key] = c
		}
		out = append(out, event.Event{ID: e.ID + ":company", TenantID: tenant, ExternalID: e.ExternalID, Name: event.CompanyLinked,
			Ts: e.Ts, Props: companyProps(c, "identify")})
	}
	return out, nil
}

// ---- fan-out ----

// fanOut publishes mk(contact) for every contact whose key attribute (company_id or stage) is
// value, a page at a time, in the background. Each event applies only if the contact still
// matches when the profile role gets to it, so a contact that moved on meanwhile is left alone.
func (s *Server) fanOut(tenant int64, key, value string, mk func(ext string) event.Event) {
	s.bg(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fanOutTimeLimit)
		defer cancel()
		var after uint32
		for {
			exts, last, err := s.accounts().MemberIDs(ctx, tenant, key, value, after, fanOutPage)
			if err != nil {
				s.Log.Error("fan out", "tenant", tenant, "key", key, "err", err)
				return
			}
			evs := make([]event.Event, len(exts))
			for i, ext := range exts {
				evs[i] = mk(ext)
			}
			if len(evs) > 0 {
				if err := s.publishEvents(ctx, tenant, evs); err != nil {
					s.Log.Error("fan out", "tenant", tenant, "key", key, "err", err)
					return
				}
			}
			if len(exts) < fanOutPage {
				return
			}
			after = last
		}
	})
}

// ---- duplicates and merges ----

func (s *Server) duplicateContacts(w http.ResponseWriter, r *http.Request, tenant int64) {
	q := r.URL.Query()
	by := q.Get("by")
	if by == "" {
		by = "email"
	}
	if by != "email" && by != "phone" {
		writeErr(w, http.StatusBadRequest, "by: email or phone")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > maxDupGroups {
		limit = 50
	}
	groups, err := s.accounts().DuplicateContacts(r.Context(), tenant, by, limit, dupPerGroup)
	if err != nil {
		s.fail(w, err)
		return
	}
	if groups == nil {
		groups = []pg.DuplicateGroup{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"by": by, "groups": groups, "more": len(groups) == limit})
}

// mergeLock reserves a merge of loser into winner until the profile role has done it: a contact
// can't be merged away twice, and a contact being merged away can't receive a merge, even when
// two requests race (merges of different winners are processed on different partitions).
// Several merges into one winner are fine: its events are processed in order.
var mergeLock = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 or redis.call('EXISTS', KEYS[2]) == 1 or redis.call('EXISTS', KEYS[3]) == 1 then
  return 0
end
redis.call('SET', KEYS[3], 1, 'PX', ARGV[1])
redis.call('INCR', KEYS[4])
redis.call('PEXPIRE', KEYS[4], ARGV[1])
return 1`)

func mergeKeys(tenant int64, winner, loser string) []string {
	t := strconv.FormatInt(tenant, 10)
	key := func(role, ext string) string { return "merge:{" + t + "}:" + role + ":" + ext }
	// KEYS: winner being merged away, loser receiving a merge, loser being merged away, winner receiving.
	return []string{key("from", winner), key("into", loser), key("from", loser), key("into", winner)}
}

// mergeContacts merges contact loser into contact winner: the winner keeps its values and gains the
// loser's other attributes, tags, points, event history, counters and list memberships; the loser
// is marked merged_into the winner and leaves every segment. Its later events go to the winner.
func (s *Server) mergeContacts(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		Winner string `json:"winner"`
		Loser  string `json:"loser"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Winner, req.Loser = strings.TrimSpace(req.Winner), strings.TrimSpace(req.Loser)
	switch {
	case req.Winner == "" || req.Loser == "":
		writeErr(w, http.StatusBadRequest, "send winner and loser: the external ids of the contact to keep and the one to merge into it")
		return
	case req.Winner == req.Loser:
		writeErr(w, http.StatusBadRequest, "a contact can't be merged into itself")
		return
	}
	winner, ok := s.knownContact(w, r, tenant, req.Winner)
	if !ok {
		return
	}
	loser, err := s.Store.ContactByExternalID(r.Context(), tenant, req.Loser)
	if err != nil {
		s.fail(w, err)
		return
	}
	if into := attr.String(loser.Attrs[event.AttrMergedInto]); into != "" {
		writeErr(w, http.StatusConflict, fmt.Sprintf("%s was already merged into %s", loser.ExternalID, into))
		return
	}
	// The loser's static lists: the winner joins them right after the merge.
	lists, err := s.accounts().ListSegmentsOfKind(r.Context(), tenant, "static")
	if err != nil {
		s.fail(w, err)
		return
	}
	evs := []event.Event{{ExternalID: winner.ExternalID, Name: event.MergeRequested, Props: map[string]any{"loser": loser.ExternalID, "source": "manual"}}}
	for _, l := range lists {
		in, err := s.Members.IsMember(r.Context(), tenant, l.ID, loser.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if in {
			evs = append(evs, event.Event{ExternalID: winner.ExternalID, Name: event.ListJoined,
				Props: map[string]any{"list_id": l.ID, "source": "merge", "from_external_id": loser.ExternalID}})
		}
	}
	if s.RDB != nil {
		got, err := mergeLock.Run(r.Context(), s.RDB, mergeKeys(tenant, winner.ExternalID, loser.ExternalID), mergeLockTTL.Milliseconds()).Int()
		if err != nil {
			s.fail(w, err)
			return
		}
		if got == 0 {
			writeErr(w, http.StatusConflict, "another merge of one of these contacts is in progress; try again in a few seconds")
			return
		}
	}
	if err := s.publishEvents(r.Context(), tenant, evs); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "event bus unavailable, retry")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"winner": winner.ExternalID, "loser": loser.ExternalID, "lists": len(evs) - 1})
}
