package api

// Contact views: the Contacts list's search box. Filters such as is:anonymous, segment:VIP or
// points:>50 are compiled into a segment rule, so a view matches exactly what a dynamic segment
// with that rule would (and "Save as segment" stores that rule). Free text is a quick find over
// ID, email, name and phone that isn't part of the rule. Filtered views and their counts run in
// ClickHouse with keyset pages; the unfiltered list pages through Postgres by id.

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"pulse/internal/attr"
	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/segment"
)

// ContactViewer runs filtered contact views in ClickHouse. where is a segment rule's condition
// (segment.WhereSQL, or empty for none) and text the free-text quick find.
type ContactViewer interface {
	ContactPage(ctx context.Context, tenant int64, where string, args []any, text string, before uint32, limit int) ([]uint32, error)
	ContactCount(ctx context.Context, tenant int64, where string, args []any, text string, limit int) (n uint64, approximate bool, err error)
}

// ContactLoader loads contacts by id, in any order.
type ContactLoader interface {
	ContactsByIDs(ctx context.Context, tenant int64, ids []uint32) ([]model.Contact, error)
}

const (
	maxViewFilters = 20
	// viewCountCap bounds the exact count of a filtered view; above it the count is approximate.
	viewCountCap = 10000
)

// viewFilter is one parsed filter, for the chips the app shows.
type viewFilter struct {
	Token string `json:"token"` // as typed, normalised: key:value
	Label string `json:"label"` // in words, e.g. "Segment: VIP buyers"
}

// contactView is a parsed search.
type contactView struct {
	Rule    *segment.Rule
	Text    string
	Filters []viewFilter
	// Special holds the M7 filters (contactsets.go): sets and outcomes behind the app's numbers.
	Special *viewSpecial
}

// viewLookup resolves names in filters. Each is called at most once, and only when needed.
type viewLookup struct {
	Segments func() ([]model.Segment, error)
	Users    func() ([]model.User, error)
	Me       int64 // the signed-in user (0 for an API key)
	Special  specialLookup
}

// optoutKeys are the consent flags is:unsubscribed looks at.
var optoutKeys = []string{"optout_all", "optout_email", "optout_sms", "optout_whatsapp", "optout_rcs", "optout_push"}

// viewTokens splits a search into words; double quotes keep spaces in a value (segment:"VIP buyers").
func viewTokens(q string) []string {
	var out []string
	var cur strings.Builder
	quoted, has := false, false
	for _, r := range q {
		switch {
		case r == '"':
			quoted, has = !quoted, true
		case unicode.IsSpace(r) && !quoted:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

// viewKeys are the filter names; list: is the old name of segment: (static segments were lists).
var viewKeys = map[string]bool{"is": true, "segment": true, "list": true, "tag": true, "stage": true, "company": true,
	"owner": true, "points": true, "email": true, "name": true}

// parseContactView compiles a search into a rule and free text. Words that don't start with a
// known key are free text.
func parseContactView(q string, lk viewLookup) (contactView, error) {
	var v contactView
	var rules []segment.Rule
	var text []string
	var segs []model.Segment
	var users []model.User
	segsLoaded, usersLoaded := false, false
	for _, tok := range viewTokens(q) {
		key, val, ok := strings.Cut(tok, ":")
		key = strings.ToLower(key)
		if !ok || (!viewKeys[key] && !specialKeys[key]) {
			text = append(text, tok)
			continue
		}
		val = strings.TrimSpace(val)
		if val == "" {
			return v, fmt.Errorf("%s: needs a value, like %s", key, viewExample[key])
		}
		if len(v.Filters) == maxViewFilters {
			return v, fmt.Errorf("use at most %d filters", maxViewFilters)
		}
		if specialKeys[key] {
			if v.Special == nil {
				v.Special = &viewSpecial{}
			}
			label, err := parseSpecial(v.Special, key, val, lk)
			if err != nil {
				return v, err
			}
			v.Filters = append(v.Filters, viewFilter{Token: key + ":" + quoteIfSpace(val), Label: label})
			continue
		}
		var r segment.Rule
		var label string
		switch key {
		case "is":
			var err error
			if r, label, err = isFilter(strings.ToLower(val)); err != nil {
				return v, err
			}
		case "segment", "list":
			if !segsLoaded {
				var err error
				if segs, err = lk.Segments(); err != nil {
					return v, err
				}
				segsLoaded = true
			}
			s, err := findSegment(segs, val)
			if err != nil {
				return v, err
			}
			r = segment.Rule{Segment: s.ID, Op: "in"}
			label = "Segment: " + s.Name
			if s.Kind == "static" {
				label = "Static segment: " + s.Name
			}
		case "tag":
			if !segment.ValidTag(val) {
				return v, fmt.Errorf("tag:%s: tags are letters, digits, - and _", val)
			}
			r, label = segment.Rule{Tag: val, Op: "has"}, "Tag: "+val
		case "stage":
			r, label = segment.Rule{Attr: "stage", Op: "eq", Value: val}, "Stage: "+val
		case "company":
			if id, err := strconv.ParseInt(val, 10, 64); err == nil && id > 0 {
				r, label = segment.Rule{Attr: "company_id", Op: "eq", Value: strconv.FormatInt(id, 10)}, "Company #"+val
			} else {
				r, label = segment.Rule{Attr: "company_name", Op: "eq", Value: val}, "Company: "+val
			}
		case "owner":
			id, name, err := ownerFilter(val, lk, &users, &usersLoaded)
			if err != nil {
				return v, err
			}
			if id == 0 {
				r, label = segment.Rule{Attr: "owner_id", Op: "not_exists"}, "No owner"
			} else {
				r, label = segment.Rule{Attr: "owner_id", Op: "eq", Value: strconv.FormatInt(id, 10)}, "Owner: "+name
			}
		case "points":
			var err error
			if r, label, err = pointsFilter(val); err != nil {
				return v, err
			}
		case "email":
			r, label = containsAny([]string{"email"}, val), "Email contains "+val
		case "name":
			r, label = containsAny([]string{"name", "first_name", "last_name"}, val), "Name contains "+val
		}
		rules = append(rules, r)
		v.Filters = append(v.Filters, viewFilter{Token: key + ":" + quoteIfSpace(val), Label: label})
	}
	if len(rules) > 0 {
		root := segment.Rule{All: rules}
		if err := root.Validate(); err != nil {
			return v, err
		}
		v.Rule = &root
	}
	if v.Special != nil {
		var err error
		if v.Filters, err = finishSpecial(v.Special, lk, v.Filters); err != nil {
			return v, err
		}
	}
	v.Text = strings.Join(text, " ")
	if utf8.RuneCountInString(v.Text) > 200 {
		return v, errors.New("search text is too long (200 characters at most)")
	}
	return v, nil
}

var viewExample = map[string]string{"is": "is:anonymous", "segment": "segment:\"VIP buyers\"", "list": "list:\"Webinar guests\"",
	"tag": "tag:vip", "stage": "stage:Customer", "company": "company:Acme", "owner": "owner:me", "points": "points:>50",
	"email": "email:@example.com", "name": "name:asha"}

func quoteIfSpace(s string) string {
	if strings.ContainsFunc(s, unicode.IsSpace) {
		return `"` + s + `"`
	}
	return s
}

func isFilter(val string) (segment.Rule, string, error) {
	eqTrue := func(keys ...string) segment.Rule {
		any := make([]segment.Rule, len(keys))
		for i, k := range keys {
			any[i] = segment.Rule{Attr: k, Op: "eq", Value: "true"}
		}
		return segment.Rule{Any: any}
	}
	bounced := []string{"email_bounced", "sms_bounced"}
	switch val {
	case "anonymous":
		// date_identified is stamped at the first event with a known id or an email. Profiles from
		// before it existed don't have it, so an email or a phone also counts as identified.
		return segment.Rule{All: []segment.Rule{{Attr: "date_identified", Op: "not_exists"}, {Attr: "email", Op: "eq", Value: ""},
			{Attr: "phone", Op: "eq", Value: ""}}}, "Anonymous visitors", nil
	case "identified":
		return segment.Rule{Any: []segment.Rule{{Attr: "date_identified", Op: "exists"}, {Attr: "email", Op: "ne", Value: ""},
			{Attr: "phone", Op: "ne", Value: ""}}}, "Identified contacts", nil
	case "unsubscribed":
		return eqTrue(optoutKeys...), "Unsubscribed (any channel)", nil
	case "bounced":
		return eqTrue(bounced...), "Bounced (email or SMS)", nil
	case "dnc":
		return eqTrue(append(append([]string{}, optoutKeys...), bounced...)...), "Do not contact", nil
	}
	return segment.Rule{}, "", fmt.Errorf("is:%s isn't a filter; use is:anonymous, is:identified, is:unsubscribed, is:bounced or is:dnc", val)
}

// findSegment matches an id, or a name ignoring case.
func findSegment(segs []model.Segment, val string) (model.Segment, error) {
	if id, err := strconv.ParseInt(val, 10, 64); err == nil {
		for _, s := range segs {
			if s.ID == id {
				return s, nil
			}
		}
	}
	for _, s := range segs {
		if strings.EqualFold(s.Name, val) {
			return s, nil
		}
	}
	return model.Segment{}, fmt.Errorf("there is no segment %q", val)
}

// ownerFilter resolves owner:me, owner:none, a user id, or a teammate's name or email. It returns
// 0 for "no owner".
func ownerFilter(val string, lk viewLookup, users *[]model.User, loaded *bool) (int64, string, error) {
	switch strings.ToLower(val) {
	case "none":
		return 0, "", nil
	case "me":
		if lk.Me == 0 {
			return 0, "", errors.New("owner:me needs a signed-in user; use owner:<name or email>")
		}
		return lk.Me, "me", nil
	}
	if lk.Users == nil {
		// The team list needs view access to settings; don't let the filter look teammates up.
		return 0, "", errors.New("filtering by a teammate needs view access to settings; use owner:me or owner:none")
	}
	if !*loaded {
		var err error
		if *users, err = lk.Users(); err != nil {
			return 0, "", err
		}
		*loaded = true
	}
	id, _ := strconv.ParseInt(val, 10, 64)
	for _, u := range *users {
		if u.ID == id || strings.EqualFold(u.Email, val) || (u.Name != "" && strings.EqualFold(u.Name, val)) {
			name := u.Name
			if name == "" {
				name = u.Email
			}
			return u.ID, name, nil
		}
	}
	return 0, "", fmt.Errorf("there is no teammate %q; use owner:me, owner:none or a teammate's name or email", val)
}

// pointsFilter reads >N, >=N, <N, <=N, =N or N. Contacts without points have 0, so when 0 passes
// the comparison they are included too.
func pointsFilter(val string) (segment.Rule, string, error) {
	op, num := "eq", val
	for _, p := range []struct{ prefix, op string }{{">=", "gte"}, {"<=", "lte"}, {">", "gt"}, {"<", "lt"}, {"=", "eq"}} {
		if rest, ok := strings.CutPrefix(val, p.prefix); ok {
			op, num = p.op, rest
			break
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil || n != n {
		return segment.Rule{}, "", fmt.Errorf("points:%s: use a number with >, >=, <, <= or =, like points:>50", val)
	}
	var r segment.Rule
	if op == "eq" {
		r = segment.Rule{Attr: "points", Op: "eq", Value: strconv.FormatFloat(n, 'f', -1, 64)}
	} else {
		r = segment.Rule{Attr: "points", Op: op, Value: n}
	}
	words := map[string]string{"eq": "=", "gt": ">", "gte": "≥", "lt": "<", "lte": "≤"}
	label := "Points " + words[op] + " " + strconv.FormatFloat(n, 'f', -1, 64)
	zero := map[string]bool{"eq": n == 0, "gt": 0 > n, "gte": 0 >= n, "lt": 0 < n, "lte": 0 <= n}[op]
	if zero {
		r = segment.Rule{Any: []segment.Rule{r, {Attr: "points", Op: "not_exists"}}}
	}
	return r, label, nil
}

// containsAny matches val in any of the attributes. Segment rules compare case-sensitively, so
// the value is tried as typed, in lower case and capitalised ("asha k" also finds "Asha K").
func containsAny(keys []string, val string) segment.Rule {
	variants := []string{val}
	for _, v := range []string{strings.ToLower(val), capitalise(strings.ToLower(val))} {
		if !contains(variants, v) {
			variants = append(variants, v)
		}
	}
	var any []segment.Rule
	for _, k := range keys {
		for _, v := range variants {
			any = append(any, segment.Rule{Attr: k, Op: "contains", Value: v})
		}
	}
	if len(any) == 1 {
		return any[0]
	}
	return segment.Rule{Any: any}
}

// capitalise upper-cases the first letter of every word.
func capitalise(s string) string {
	words := strings.Split(s, " ")
	for i, w := range words {
		if r, n := utf8.DecodeRuneInString(w); n > 0 {
			words[i] = string(unicode.ToUpper(r)) + w[n:]
		}
	}
	return strings.Join(words, " ")
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// parseViewRequest reads ?q= for the signed-in principal.
func (s *Server) parseViewRequest(r *http.Request, p model.Principal) (contactView, error) {
	lk := viewLookup{
		Segments: func() ([]model.Segment, error) { return s.Store.ListSegments(r.Context(), p.TenantID) },
		Me:       p.UserID,
	}
	if p.Perms.Allows(auth.AreaSettings, auth.View) { // the same access GET /v1/users needs
		lk.Users = func() ([]model.User, error) { return s.Store.ListUsers(r.Context(), p.TenantID) }
	}
	lk.Special = s.specialLookup(r.Context(), p)
	return parseContactView(r.URL.Query().Get("q"), lk)
}

// specialLookup resolves the names in M7 filters for the caller's workspace.
func (s *Server) specialLookup(ctx context.Context, p model.Principal) specialLookup {
	tenant := p.TenantID
	sl := specialLookup{Allowed: func(f string) bool { return viewFilterAllowed(p.Perms, f) }, Today: s.now()}
	if bs, ok := s.Store.(BroadcastStore); ok {
		sl.Broadcast = func(id int64) (string, error) {
			b, err := bs.GetBroadcast(ctx, tenant, id)
			return b.Name, err
		}
	}
	if us, ok := s.Store.(UIStore); ok {
		sl.Journey = func(id int64) (model.Journey, error) { return us.GetJourney(ctx, tenant, id) }
		sl.Journeys = func() ([]model.Journey, error) { return us.ListJourneys(ctx, tenant) }
		sl.Template = func(id int64) (string, error) {
			t, err := us.Template(ctx, tenant, id)
			return t.Name, err
		}
	}
	if fs, ok := s.Store.(FormStore); ok {
		sl.Form = func(id int64) (string, error) {
			f, err := fs.GetForm(ctx, tenant, id)
			return f.Name, err
		}
	}
	return sl
}

// searchContacts: GET /v1/contacts?q=&after=&limit=. Without filters or text it pages through
// Postgres by id; a view with set filters (M7: in:, reachable:, journey:…) pages through their
// Redis bitmap; otherwise ClickHouse finds the ids. The first page of a view carries its count
// (exact up to 10,000, approximate above, BITCOUNT for plain set views), the parsed filters, a
// note on why the count may differ from where it was opened, and with rule filters only, the
// equivalent segment rule.
func (s *Server) searchContacts(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	after, _ := strconv.ParseUint(q.Get("after"), 10, 32)
	view, err := s.parseViewRequest(r, p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	special := view.Special != nil && view.Special.any()
	out := map[string]any{"filters": view.Filters, "text": view.Text}
	if view.Rule != nil && !special {
		out["rule"] = view.Rule
	}
	if special {
		out["note"] = viewNote(view.Special)
	}
	var cs []model.Contact
	switch {
	case view.Rule == nil && view.Text == "" && !special:
		cs, err = s.uiStore().ListContacts(r.Context(), tenant, uint32(after), limit)
		if err != nil {
			s.fail(w, err)
			return
		}
	default:
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if special && after == 0 && !view.Special.bitmap() && !s.allowTenant(ctx, "rlview", tenant, viewsPerMin) {
			writeErr(w, http.StatusTooManyRequests, errTooMany.Error())
			return
		}
		where, args, err := s.viewWhereAll(view, tenant)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		var ids []uint32
		if special && view.Special.bitmap() {
			sv, err := s.buildSetView(ctx, tenant, view, where, args)
			if err != nil {
				s.viewErr(w, err)
				return
			}
			if ids, err = bitmapIDs(ctx, s.RDB, sv.key, uint32(after), limit, sv.keep); err != nil {
				s.fail(w, err)
				return
			}
			if after == 0 {
				n, approx, err := s.countSetView(ctx, sv)
				if err != nil {
					s.fail(w, err)
					return
				}
				out["count"], out["approximate"] = n, approx
			}
		} else {
			cv, ok := s.Analytics.(ContactViewer)
			if !ok {
				writeErr(w, http.StatusServiceUnavailable, "analytics unavailable")
				return
			}
			if ids, err = cv.ContactPage(ctx, tenant, where, args, view.Text, uint32(after), limit); err != nil {
				s.fail(w, err)
				return
			}
			if after == 0 {
				n, approx, err := cv.ContactCount(ctx, tenant, where, args, view.Text, viewCountCap)
				if err != nil {
					s.fail(w, err)
					return
				}
				out["count"], out["approximate"] = n, approx
			}
		}
		if cs, err = s.loadInOrder(ctx, tenant, ids); err != nil {
			s.fail(w, err)
			return
		}
		if len(ids) == limit {
			out["next"] = strconv.FormatUint(uint64(ids[len(ids)-1]), 10)
		}
	}
	if cs == nil {
		cs = []model.Contact{}
	}
	if _, set := out["next"]; !set {
		out["next"] = ""
		if view.Rule == nil && view.Text == "" && !special && len(cs) == limit {
			out["next"] = strconv.FormatUint(uint64(cs[len(cs)-1].ID), 10)
		}
	}
	out["contacts"] = cs
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) viewWhere(v contactView, tenant int64) (string, []any, error) {
	if v.Rule == nil {
		return "", nil, nil
	}
	return segment.WhereSQL(*v.Rule, tenant, s.now())
}

// loadInOrder loads contacts from Postgres in the order of ids (a contact ClickHouse knows but
// Postgres doesn't, which can't normally happen, is left out).
func (s *Server) loadInOrder(ctx context.Context, tenant int64, ids []uint32) ([]model.Contact, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ld, ok := s.Store.(ContactLoader)
	if !ok {
		return nil, errors.New("contact loader unavailable")
	}
	cs, err := ld.ContactsByIDs(ctx, tenant, ids)
	if err != nil {
		return nil, err
	}
	pos := make(map[uint32]int, len(ids))
	for i, id := range ids {
		pos[id] = i
	}
	sort.Slice(cs, func(i, j int) bool { return pos[cs[i].ID] < pos[cs[j].ID] })
	return cs, nil
}

// exportContacts: GET /v1/contacts/export?q=&columns=. Streams the contacts of a view (all of
// them without q) as CSV: external_id, email, created_at, updated_at, then the chosen attribute
// columns (every attribute without columns). Cells are defused against formula injection.
func (s *Server) exportContacts(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	view, err := s.parseViewRequest(r, p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var keys []string
	if cols := strings.TrimSpace(r.URL.Query().Get("columns")); cols != "" {
		fixed := map[string]bool{"external_id": true, "email": true, "created_at": true, "updated_at": true}
		seen := map[string]bool{}
		for _, k := range strings.Split(cols, ",") {
			k = strings.TrimSpace(k)
			if fixed[k] || seen[k] || k == "" {
				continue
			}
			if !attr.ValidKey(k) {
				writeErr(w, http.StatusBadRequest, fmt.Sprintf("invalid column %q", k))
				return
			}
			seen[k] = true
			keys = append(keys, k)
		}
	} else {
		if keys, err = s.Store.(ExportStore).AttrKeys(r.Context(), tenant); err != nil {
			s.fail(w, err)
			return
		}
		sort.Strings(keys)
	}
	filtered := view.Rule != nil || view.Text != "" || (view.Special != nil && view.Special.any())
	var page func(ctx context.Context, after uint32, limit int) ([]uint32, error)
	if filtered {
		if view.Special != nil && view.Special.any() && !s.allowTenant(r.Context(), "rlview", tenant, viewsPerMin) {
			writeErr(w, http.StatusTooManyRequests, errTooMany.Error())
			return
		}
		if page, _, _, err = s.viewPager(r.Context(), tenant, view, false); err != nil {
			s.viewErr(w, err)
			return
		}
	}
	name := "contacts"
	if filtered {
		name = "contacts-view"
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.csv"`, name, s.now().Format("2006-01-02")))
	cw := csv.NewWriter(w)
	_ = cw.Write(append([]string{"external_id", "email", "created_at", "updated_at"}, keys...))
	const pageSize = 1000
	var after uint32
	for {
		var cs []model.Contact
		var more bool
		if filtered {
			ids, err := page(r.Context(), after, pageSize)
			if err == nil {
				cs, err = s.loadInOrder(r.Context(), tenant, ids)
			}
			if err != nil {
				s.Log.Error("export", "err", err) // headers are sent: the file ends early
				return
			}
			more = len(ids) == pageSize
			if more {
				after = ids[len(ids)-1]
			}
		} else {
			cs, err = s.uiStore().ListContacts(r.Context(), tenant, after, pageSize)
			if err != nil {
				s.Log.Error("export", "err", err)
				return
			}
			more = len(cs) == pageSize
			if more {
				after = cs[len(cs)-1].ID
			}
		}
		for _, c := range cs {
			row := []string{csvSafe(c.ExternalID), csvSafe(c.Email), c.CreatedAt.UTC().Format(time.RFC3339), c.UpdatedAt.UTC().Format(time.RFC3339)}
			for _, k := range keys {
				v, ok := c.Attrs[k]
				if !ok {
					row = append(row, "")
					continue
				}
				if m, isMap := v.(map[string]any); isMap {
					b, _ := json.Marshal(m)
					row = append(row, string(b))
					continue
				}
				row = append(row, csvSafe(attr.String(v)))
			}
			_ = cw.Write(row)
		}
		cw.Flush()
		if !more {
			return
		}
	}
}
