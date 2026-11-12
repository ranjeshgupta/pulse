package api

// AMP email: checking and previewing a template's AMP part in the editor, and the public post-back
// endpoints its forms and polls call from inside the email client.
//
//	POST /v1/templates/amp/check        validate AMP code or build it from the visual blocks (content view)
//	GET  /v1/templates/{id}/amp/results poll results of a template, per block (content view)
//	POST /v1/amp/{token}                a form or poll answer, from the email client (public, signed token)
//	GET  /v1/amp/{token}/results        a poll's results for <amp-list> (public, signed token)
//
// The token (tracking.AMPToken) is sealed by this server and names the workspace, contact, message,
// template and block: nothing in the request body chooses who answered or where it's recorded.
// Requests must come from an email client on behalf of one of the workspace's sending addresses
// (AMP for Email CORS: AMP-Email-Sender, or Origin + __amp_source_origin), and are rate-limited.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/mail"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"pulse/internal/amp"
	"pulse/internal/channel"
	"pulse/internal/event"
	"pulse/internal/eventschema"
	"pulse/internal/model"
	"pulse/internal/providers"
	"pulse/internal/store/pg"
	"pulse/internal/tracking"
)

// Events recorded from AMP emails (starter schemas in eventschema).
const (
	EventAMPForm = event.AMPFormSubmitted
	EventAMPPoll = event.AMPPollAnswered
)

const (
	ampPerMinuteIP    = 60 // answers per client IP and workspace per minute
	ampPerMinuteToken = 10 // answers per token per minute
	ampMaxBody        = 64 << 10
	ampMaxValue       = 1000
	ampMaxFields      = 20
	ampPollTTL        = 400 * 24 * time.Hour
)

// ampOrigins are the email clients that send AMP for Email requests with an Origin (CORS v1).
var ampOrigins = []string{"https://mail.google.com", "https://amp.gmail.dev", "https://playground.amp.dev",
	"https://mail.yahoo.com", "https://mail.yahoo.co.jp", "https://outlook.live.com", "https://outlook.office.com",
	"https://outlook.office365.com", "https://e.mail.ru", "https://touch.mail.ru", "https://octavius.mail.ru"}

var ampFieldRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

func (s *Server) ampRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/templates/amp/check", s.tenant(s.ampCheck))
	mux.HandleFunc("GET /v1/templates/{id}/amp/results", s.tenant(s.ampTemplateResults))
	mux.HandleFunc("OPTIONS /v1/amp/{token}", s.ampPreflight)
	mux.HandleFunc("POST /v1/amp/{token}", s.ampSubmit)
	mux.HandleFunc("OPTIONS /v1/amp/{token}/results", s.ampPreflight)
	mux.HandleFunc("GET /v1/amp/{token}/results", s.ampResults)
}

// ---- editor ----

// ampTemplates reads templates (pg.Store; a narrow interface for tests).
type ampTemplates interface {
	Template(ctx context.Context, tenant, id int64) (model.Template, error)
}

type ampProvider struct {
	Name string `json:"name"`
	AMP  bool   `json:"amp"`
	Note string `json:"note,omitempty"`
}

// ampCheck validates AMP code (or builds it from blocks) and returns the problems with line
// numbers, a script-free preview, and which of the workspace's email providers send AMP.
func (s *Server) ampCheck(w http.ResponseWriter, r *http.Request, tenant int64) {
	var req struct {
		AMP       string          `json:"amp"`
		AMPBlocks json.RawMessage `json:"amp_blocks"`
	}
	if !decode(w, r, &req) {
		return
	}
	resp := map[string]any{}
	problems := []amp.Problem{}
	code := req.AMP
	if d, err := amp.ParseDoc(req.AMPBlocks); err != nil {
		problems = append(problems, amp.Problem{Message: err.Error()})
	} else if d != nil {
		if code, err = d.Build(); err != nil {
			for _, e := range strings.Split(err.Error(), "\n") {
				problems = append(problems, amp.Problem{Message: e})
			}
		}
		if err := s.checkSaveTargets(d); err != nil {
			problems = append(problems, amp.Problem{Message: err.Error()})
		}
	}
	problems = append(problems, amp.Validate(code)...)
	preview := ""
	if code != "" {
		content, _ := json.Marshal(channel.Content{AMP: code})
		out, err := channel.Render(model.Template{Channel: channel.Email, Subject: "x", HTML: "x", Content: content}, sampleContact)
		if err != nil {
			problems = append(problems, amp.Problem{Message: err.Error()})
		} else {
			preview = amp.PreviewHTML(amp.Fill(out.Content.AMP, func(string, string) string { return "#" }))
		}
	}
	provs := []ampProvider{}
	if cs, ok := s.Store.(interface {
		ListProviders(ctx context.Context, tenant int64) ([]model.Provider, error)
	}); !ok {
	} else if ps, err := cs.ListProviders(r.Context(), tenant); err == nil {
		for _, p := range ps {
			if p.Channel != channel.Email {
				continue
			}
			ok, note := providers.SupportsAMP(p.Kind, p.Config)
			provs = append(provs, ampProvider{Name: p.Name, AMP: ok, Note: note})
		}
	}
	resp["amp"], resp["problems"], resp["size"], resp["max_size"] = code, problems, len(code), amp.MaxBytes
	resp["preview"], resp["providers"], resp["blocks"] = preview, provs, nonNil(amp.Blocks(code))
	writeJSON(w, http.StatusOK, resp)
}

// checkSaveTargets: form fields may copy answers only into custom contact fields, never Pulse's
// own, consent or identity fields.
func (s *Server) checkSaveTargets(d *amp.Doc) error {
	for _, k := range d.SaveTargets() {
		if !eventschema.ListTargetAllowed(k) {
			return fmt.Errorf("a form field can't save into %q: choose one of your own contact fields (not email, phone, consent or Pulse's fields)", k)
		}
	}
	return nil
}

// prepareTemplateAMP builds a template's AMP part from its blocks and checks where form answers go.
func (s *Server) prepareTemplateAMP(t *model.Template) error {
	if err := channel.PrepareAMP(t); err != nil {
		return err
	}
	c, err := channel.ParseContent(t.Content)
	if err != nil {
		return err
	}
	d, err := amp.ParseDoc(c.AMPBlocks)
	if err != nil || d == nil {
		return err
	}
	return s.checkSaveTargets(d)
}

func (s *Server) ampTemplateResults(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := s.Store.(ampTemplates).Template(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{}
	d := templateDoc(t)
	if d != nil {
		for _, b := range d.Blocks {
			if b.Type == amp.TypePoll {
				items, total := s.pollResults(r.Context(), tenant, t.ID, b)
				out[b.ID] = map[string]any{"question": b.Text, "items": items, "total": total}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"polls": out})
}

// checkTemplateSaveTargets checks a saved template's form fields (also for templates saved without
// going through prepareTemplateAMP, such as duplicates).
func (s *Server) checkTemplateSaveTargets(t model.Template) error {
	if d := templateDoc(t); d != nil {
		return s.checkSaveTargets(d)
	}
	return nil
}

func templateDoc(t model.Template) *amp.Doc {
	c, err := channel.ParseContent(t.Content)
	if err != nil {
		return nil
	}
	d, _ := amp.ParseDoc(c.AMPBlocks)
	return d
}

// ---- post-back ----

type pollItem struct {
	Label   string `json:"label"`
	Count   int64  `json:"count"`
	Percent int    `json:"percent"`
}

func pollKey(tenant, template int64, block string) string {
	return "amp:poll:" + strconv.FormatInt(tenant, 10) + ":" + strconv.FormatInt(template, 10) + ":" + block
}

// pollResults reads a poll's answer counts (in the poll's answer order).
func (s *Server) pollResults(ctx context.Context, tenant, template int64, b amp.Block) ([]pollItem, int64) {
	items := make([]pollItem, 0, len(b.Options))
	counts := map[string]string{}
	if s.RDB != nil {
		counts, _ = s.RDB.HGetAll(ctx, pollKey(tenant, template, b.ID)).Result()
	}
	var total int64
	for _, o := range b.Options {
		n, _ := strconv.ParseInt(counts[strings.TrimSpace(o)], 10, 64)
		total += n
		items = append(items, pollItem{Label: strings.TrimSpace(o), Count: n})
	}
	for i := range items {
		if total > 0 {
			items[i].Percent = int(math.Round(float64(items[i].Count) * 100 / float64(total)))
		}
	}
	return items, total
}

// sendingDomains are the domains of the workspace's email From addresses (and the server's
// default sender), cached for a minute.
func (s *Server) sendingDomains(ctx context.Context, tenant int64) map[string]bool {
	ampDomains.Lock()
	c, ok := ampDomains.m[tenant]
	ampDomains.Unlock()
	if ok && s.now().Before(c.exp) {
		return c.domains
	}
	out := map[string]bool{}
	add := func(from string) {
		if a, err := mail.ParseAddress(from); err == nil {
			if _, d, ok := strings.Cut(a.Address, "@"); ok {
				out[strings.ToLower(d)] = true
			}
		}
	}
	add(s.DefaultFrom)
	if cs, ok := s.Store.(interface {
		ListProviders(ctx context.Context, tenant int64) ([]model.Provider, error)
	}); ok {
		if ps, err := cs.ListProviders(ctx, tenant); err == nil {
			for _, p := range ps {
				if p.Channel == channel.Email {
					add(p.Config["from"])
				}
			}
		}
	}
	ampDomains.Lock()
	ampDomains.m[tenant] = cachedDomains{domains: out, exp: s.now().Add(time.Minute)}
	ampDomains.Unlock()
	return out
}

type cachedDomains struct {
	domains map[string]bool
	exp     time.Time
}

var ampDomains = struct {
	sync.Mutex
	m map[int64]cachedDomains
}{m: map[int64]cachedDomains{}}

// senderAllowed: the email was sent from one of the workspace's sending domains.
func (s *Server) senderAllowed(ctx context.Context, tenant int64, sender string) bool {
	a, err := mail.ParseAddress(sender)
	if err != nil {
		return false
	}
	_, d, ok := strings.Cut(a.Address, "@")
	return ok && s.sendingDomains(ctx, tenant)[strings.ToLower(d)]
}

// ampCORS applies the AMP for Email CORS rules: version 2 (AMP-Email-Sender → AMP-Email-Allow-Sender)
// or version 1 (Origin of a known email client, __amp_source_origin = the sender's address →
// Access-Control-Allow-Origin and AMP-Access-Control-Allow-Source-Origin). The sender must be
// one of the workspace's sending addresses' domains.
func (s *Server) ampCORS(w http.ResponseWriter, r *http.Request, tenant int64) error {
	h := w.Header()
	if sender := strings.TrimSpace(r.Header.Get("AMP-Email-Sender")); sender != "" {
		if !s.senderAllowed(r.Context(), tenant, sender) {
			return errors.New("sender not allowed")
		}
		h.Set("AMP-Email-Allow-Sender", sender)
		return nil
	}
	src := r.URL.Query().Get("__amp_source_origin")
	origin := r.Header.Get("Origin")
	if src == "" || origin == "" {
		return errors.New("not an AMP email request")
	}
	if !slices.Contains(ampOrigins, origin) {
		return errors.New("origin not allowed")
	}
	if !s.senderAllowed(r.Context(), tenant, src) {
		return errors.New("sender not allowed")
	}
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Allow-Credentials", "true")
	h.Set("AMP-Access-Control-Allow-Source-Origin", src)
	h.Set("Access-Control-Expose-Headers", "AMP-Access-Control-Allow-Source-Origin")
	h.Add("Vary", "Origin")
	return nil
}

func (s *Server) ampPreflight(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); slices.Contains(ampOrigins, o) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", o)
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Set("Access-Control-Allow-Methods", "GET, POST")
		h.Set("Access-Control-Allow-Headers", "Content-Type, AMP-Email-Sender")
		h.Set("Access-Control-Max-Age", "600")
		h.Add("Vary", "Origin")
	}
	w.WriteHeader(http.StatusNoContent)
}

// ampMessage answers with a {"message": …} the AMP templates show.
func ampMessage(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"message": msg})
}

// ampToken opens and checks the request's token, then the CORS rules and rate limits. On failure
// it has answered.
func (s *Server) ampToken(w http.ResponseWriter, r *http.Request) (tracking.AMPToken, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if s.Tracking == nil {
		ampMessage(w, http.StatusNotFound, "This form isn't available.")
		return tracking.AMPToken{}, false
	}
	tok, err := s.Tracking.DecodeAMP(r.PathValue("token"))
	if err != nil {
		ampMessage(w, http.StatusBadRequest, "This form isn't valid.")
		return tok, false
	}
	if err := s.ampCORS(w, r, tok.TenantID); err != nil {
		ampMessage(w, http.StatusForbidden, "This form can only be used from the email it was sent in.")
		return tok, false
	}
	if s.now().Unix() > tok.Exp {
		ampMessage(w, http.StatusGone, "This form has expired.")
		return tok, false
	}
	sum := sha256.Sum256([]byte(r.PathValue("token")))
	if !s.allowRate(r.Context(), "rlamp", tok.TenantID, r, 1, ampPerMinuteIP) ||
		!s.allowTenant(r.Context(), "rlampt:"+hex.EncodeToString(sum[:8]), tok.TenantID, ampPerMinuteToken) {
		ampMessage(w, http.StatusTooManyRequests, "Too many answers. Please try again in a minute.")
		return tok, false
	}
	return tok, true
}

func (s *Server) ampResults(w http.ResponseWriter, r *http.Request) {
	tok, ok := s.ampToken(w, r)
	if !ok {
		return
	}
	t, err := s.Store.(ampTemplates).Template(r.Context(), tok.TenantID, tok.TemplateID)
	b, found := templateDoc(t).Find(tok.Block)
	if err != nil || !found || b.Type != amp.TypePoll {
		ampMessage(w, http.StatusNotFound, "This poll is no longer available.")
		return
	}
	items, total := s.pollResults(r.Context(), tok.TenantID, t.ID, b)
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) ampSubmit(w http.ResponseWriter, r *http.Request) {
	tok, ok := s.ampToken(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, ampMaxBody)
	var perr error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		perr = r.ParseMultipartForm(ampMaxBody)
	} else {
		perr = r.ParseForm()
	}
	if perr != nil {
		ampMessage(w, http.StatusBadRequest, "Couldn't read your answer.")
		return
	}
	form := r.PostForm // the body only: the query carries __amp_source_origin
	t, err := s.Store.(ampTemplates).Template(r.Context(), tok.TenantID, tok.TemplateID)
	if errors.Is(err, pg.ErrNotFound) {
		ampMessage(w, http.StatusNotFound, "This form is no longer available.")
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	doc := templateDoc(t)
	b, found := doc.Find(tok.Block)
	if !found {
		// Hand-written AMP: a form that posts to this block is recorded as a form submission.
		c, _ := channel.ParseContent(t.Content)
		if !slices.Contains(amp.Blocks(c.AMP), tok.Block) {
			ampMessage(w, http.StatusNotFound, "This form is no longer available.")
			return
		}
		b = amp.Block{ID: tok.Block, Type: amp.TypeForm}
	}
	props := tok.Token.Props()
	props["block"] = tok.Block
	if b.Type == amp.TypePoll {
		s.ampPoll(w, r, tok, t, b, form, props)
		return
	}
	if b.Type != amp.TypeForm {
		ampMessage(w, http.StatusNotFound, "This form is no longer available.")
		return
	}
	attrs := map[string]any{}
	if len(b.Fields) > 0 {
		for _, f := range b.Fields {
			v := strings.TrimSpace(form.Get(f.Key))
			if len(v) > ampMaxValue {
				v = v[:ampMaxValue]
			}
			if f.Required && v == "" {
				ampMessage(w, http.StatusBadRequest, "Please fill in "+f.Label+".")
				return
			}
			if f.Type == "email" && v != "" {
				if _, err := mail.ParseAddress(v); err != nil {
					ampMessage(w, http.StatusBadRequest, f.Label+" isn't a valid email address.")
					return
				}
			}
			var val any = v
			switch f.Type {
			case "checkbox":
				val = v != ""
			case "number":
				if v != "" {
					n, err := strconv.ParseFloat(v, 64)
					if err != nil {
						ampMessage(w, http.StatusBadRequest, f.Label+" must be a number.")
						return
					}
					val = n
				}
			case "select":
				if v != "" && !slices.Contains(f.Options, v) {
					ampMessage(w, http.StatusBadRequest, "Choose one of the options for "+f.Label+".")
					return
				}
			}
			if v == "" && f.Type != "checkbox" {
				continue
			}
			props[f.Key] = val
			// Only the template's own form settings choose a contact field, and only a custom one.
			if f.SaveTo != "" && eventschema.ListTargetAllowed(f.SaveTo) {
				attrs[f.SaveTo] = val
			}
		}
	} else {
		keys := make([]string, 0, len(form))
		for k := range form {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(props) >= ampMaxFields+8 {
				break
			}
			if !ampFieldRe.MatchString(k) || props[k] != nil {
				continue
			}
			v := strings.TrimSpace(form.Get(k))
			if len(v) > ampMaxValue {
				v = v[:ampMaxValue]
			}
			props[k] = v
		}
	}
	msg := or(b.SuccessText, "Thanks! We've got your answers.")
	if tok.Test {
		ampMessage(w, http.StatusOK, msg+" (Test email: nothing was recorded.)")
		return
	}
	vals, _ := json.Marshal(props)
	ev := event.Event{ID: ampEventID(tok, string(vals)), ExternalID: tok.ExternalID, Name: EventAMPForm, Props: props}
	if len(attrs) > 0 {
		ev.Attrs = attrs
	}
	if err := s.publishEvents(r.Context(), tok.TenantID, []event.Event{ev}); err != nil {
		ampMessage(w, http.StatusServiceUnavailable, "Couldn't save your answers. Please try again.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}

func or(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// ampEventID makes repeated posts of the same answer record once.
func ampEventID(tok tracking.AMPToken, extra string) string {
	sum := sha256.Sum256([]byte(fmt.Sprint(tok.TenantID, "|", tok.MessageID, "|", tok.ContactID, "|", tok.Block, "|", extra)))
	return "amp-" + hex.EncodeToString(sum[:16])
}

// ampPoll records a poll answer: one answer per contact per poll (the first one counts), then the
// results.
func (s *Server) ampPoll(w http.ResponseWriter, r *http.Request, tok tracking.AMPToken, t model.Template, b amp.Block, form map[string][]string, props map[string]any) {
	answer := ""
	if v := form["answer"]; len(v) > 0 {
		answer = strings.TrimSpace(v[0])
	}
	valid := false
	for _, o := range b.Options {
		if strings.TrimSpace(o) == answer {
			valid = true
		}
	}
	if !valid {
		ampMessage(w, http.StatusBadRequest, "Choose one of the answers.")
		return
	}
	msg := or(b.SuccessText, "Thanks for voting!")
	already := false
	if !tok.Test && s.RDB != nil {
		who := tok.ExternalID
		if tok.ContactID != 0 {
			who = strconv.FormatUint(uint64(tok.ContactID), 10)
		}
		ansKey := "amp:ans:" + strconv.FormatInt(tok.TenantID, 10) + ":" + strconv.FormatInt(t.ID, 10) + ":" + b.ID + ":" + who
		set, err := s.RDB.SetNX(r.Context(), ansKey, answer, ampPollTTL).Result()
		if err != nil {
			ampMessage(w, http.StatusServiceUnavailable, "Couldn't save your answer. Please try again.")
			return
		}
		if set {
			key := pollKey(tok.TenantID, t.ID, b.ID)
			p := s.RDB.TxPipeline()
			p.HIncrBy(r.Context(), key, answer, 1)
			p.Expire(r.Context(), key, ampPollTTL)
			if _, err := p.Exec(r.Context()); err != nil {
				s.RDB.Del(r.Context(), ansKey)
				ampMessage(w, http.StatusServiceUnavailable, "Couldn't save your answer. Please try again.")
				return
			}
			props["question"], props["answer"] = b.Text, answer
			ev := event.Event{ID: ampEventID(tok, ""), ExternalID: tok.ExternalID, Name: EventAMPPoll, Props: props}
			if err := s.publishEvents(r.Context(), tok.TenantID, []event.Event{ev}); err != nil {
				s.Log.Error("record amp poll answer", "err", err)
			}
		} else {
			already = true
			prev, _ := s.RDB.Get(r.Context(), ansKey).Result()
			msg = "You already answered: " + prev
		}
	} else if tok.Test {
		msg += " (Test email: nothing was recorded.)"
	}
	resp := map[string]any{"message": msg, "already": already}
	if b.ShowResults {
		resp["items"], resp["total"] = s.pollResults(r.Context(), tok.TenantID, t.ID, b)
	}
	writeJSON(w, http.StatusOK, resp)
}
