package api

// Broadcasts, template previews with a real contact's data, and provider connection tests.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"pulse/internal/amp"
	"pulse/internal/assets"
	"pulse/internal/auth"
	"pulse/internal/broadcast"
	"pulse/internal/channel"
	"pulse/internal/dispatch"
	"pulse/internal/event"
	"pulse/internal/model"
	"pulse/internal/personalize"
	"pulse/internal/providers"
	"pulse/internal/schedule"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
)

type BroadcastStore interface {
	SaveBroadcast(ctx context.Context, b model.Broadcast) (int64, error)
	GetBroadcast(ctx context.Context, tenant, id int64) (model.Broadcast, error)
	ListBroadcasts(ctx context.Context, tenant int64) ([]model.Broadcast, error)
	DeleteBroadcast(ctx context.Context, tenant, id int64) error
	SetBroadcastSchedule(ctx context.Context, tenant, id int64, status string, next *time.Time) error
	BroadcastRuns(ctx context.Context, tenant, id int64) ([]model.BroadcastRun, error)
	PickWinner(ctx context.Context, tenant, broadcast, run int64, variant string) error
}

func (s *Server) broadcastRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/broadcasts", s.tenant(s.listBroadcasts))
	mux.HandleFunc("POST /v1/broadcasts", s.tenant(s.saveBroadcast))
	mux.HandleFunc("GET /v1/broadcasts/{id}", s.tenant(s.getBroadcast))
	mux.HandleFunc("PUT /v1/broadcasts/{id}", s.tenant(s.saveBroadcast))
	mux.HandleFunc("DELETE /v1/broadcasts/{id}", s.tenant(s.deleteBroadcast))
	mux.HandleFunc("POST /v1/broadcasts/{id}/schedule", s.authed(s.scheduleBroadcast))
	mux.HandleFunc("POST /v1/broadcasts/{id}/cancel", s.tenant(s.cancelBroadcast))
	mux.HandleFunc("POST /v1/broadcasts/{id}/runs/{run}/winner", s.tenant(s.pickWinner))
	mux.HandleFunc("GET /v1/broadcasts/{id}/report", s.tenant(s.broadcastReport))
	mux.HandleFunc("POST /v1/templates/preview", s.authed(s.previewTemplate))
	mux.HandleFunc("POST /v1/channels/providers/{id}/test", s.admin(s.testProvider))
}

func (s *Server) bcStore() BroadcastStore { return s.Store.(BroadcastStore) }

func (s *Server) listBroadcasts(w http.ResponseWriter, r *http.Request, tenant int64) {
	bs, err := s.bcStore().ListBroadcasts(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"broadcasts": bs})
}

func (s *Server) getBroadcast(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, err := s.bcStore().GetBroadcast(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	runs, err := s.bcStore().BroadcastRuns(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"broadcast": b, "runs": runs})
}

// saveBroadcast creates or edits a broadcast; it is saved as a draft until scheduled.
func (s *Server) saveBroadcast(w http.ResponseWriter, r *http.Request, tenant int64) {
	var b model.Broadcast
	if !decode(w, r, &b) {
		return
	}
	b.TenantID, b.ID = tenant, 0
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		b.ID = id
	}
	if b.Repeat == "" {
		b.Repeat = schedule.None
	}
	if b.AB != nil && len(b.AB.Variants) > 0 {
		b.TemplateID = b.AB.Variants[0].TemplateID // the A variant stands for the broadcast's template
	}
	if b.ConversionEvent == "" {
		b.ConversionDays = 0
	} else if b.ConversionDays == 0 {
		b.ConversionDays = 3
	}
	tags, err := model.NormalizeTags(b.Tags)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	b.Tags = tags
	if err := s.validBroadcast(r.Context(), b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.bcStore().SaveBroadcast(r.Context(), b)
	if err != nil {
		if errors.Is(err, pg.ErrNotFound) {
			s.fail(w, err)
			return
		}
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "draft"})
}

func (s *Server) validBroadcast(ctx context.Context, b model.Broadcast) error {
	var errs []error
	if strings.TrimSpace(b.Name) == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if !schedule.ValidRepeat(b.Repeat) {
		errs = append(errs, errors.New("repeat must be none, daily, weekly or monthly"))
	}
	errs = append(errs, (schedule.Window{Start: b.StartAt, End: b.EndAt}).Validate())
	if _, err := s.Store.GetSegment(ctx, b.TenantID, b.SegmentID); err != nil {
		errs = append(errs, errors.New("choose a segment"))
	}
	if len(b.ExcludeSegmentIDs) > 10 || len(b.IncludeSegmentIDs) > 9 {
		errs = append(errs, errors.New("send to at most 10 segments, and exclude at most 10"))
	}
	for _, id := range b.IncludeSegmentIDs {
		if id == b.SegmentID || slices.Contains(b.ExcludeSegmentIDs, id) {
			errs = append(errs, errors.New("a segment can be in the audience once, and not also excluded"))
		} else if _, err := s.Store.GetSegment(ctx, b.TenantID, id); err != nil {
			errs = append(errs, fmt.Errorf("segment %d doesn't exist", id))
		}
	}
	errs = append(errs, b.Deadline.Validate())
	for _, id := range b.ExcludeSegmentIDs {
		if id == b.SegmentID {
			errs = append(errs, errors.New("a segment can't be both the audience and excluded"))
		} else if _, err := s.Store.GetSegment(ctx, b.TenantID, id); err != nil {
			errs = append(errs, fmt.Errorf("excluded segment %d doesn't exist", id))
		}
	}
	errs = append(errs, broadcast.ValidateLocalTime(b), broadcast.ValidateSpeed(b), broadcast.ValidateFeed(b))
	t, err := s.uiStore().Template(ctx, b.TenantID, b.TemplateID)
	if err != nil {
		errs = append(errs, errors.New("choose a template"))
	} else if t.Channel != b.Channel {
		errs = append(errs, errors.New("the template is for a different channel"))
	}
	if b.ProviderID != 0 {
		if err := s.providerFor(ctx, b.TenantID, b.ProviderID, b.Channel); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.topicProblem(ctx, b.TenantID, b.Topic); err != nil {
		errs = append(errs, err)
	}
	if b.FallbackTemplateID != 0 {
		ft, err := s.uiStore().Template(ctx, b.TenantID, b.FallbackTemplateID)
		if !channel.FallsBackToSMS(b.Channel) || err != nil || ft.Channel != channel.SMS {
			errs = append(errs, errors.New("an SMS fallback needs a WhatsApp or RCS broadcast and an SMS template"))
		}
	}
	if b.AB != nil {
		errs = append(errs, broadcast.ValidateAB(b.AB, b.Channel))
		for _, v := range b.AB.Variants {
			if vt, err := s.uiStore().Template(ctx, b.TenantID, v.TemplateID); err != nil || vt.Channel != b.Channel {
				errs = append(errs, fmt.Errorf("variant %s: choose a %s template", v.Label, b.Channel))
			}
		}
	}
	if b.ConversionEvent != "" {
		if !event.ValidName(b.ConversionEvent) {
			errs = append(errs, errors.New("the conversion event name is invalid"))
		}
		if b.ConversionDays < 1 || b.ConversionDays > 30 {
			errs = append(errs, errors.New("count conversions within 1 to 30 days"))
		}
	}
	return errors.Join(errs...)
}

// pickWinner chooses an A/B run's winner by hand (before an automatic pick, or for "manual" tests).
func (s *Server) pickWinner(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, err := strconv.ParseInt(r.PathValue("run"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid run")
		return
	}
	var req struct {
		Variant string `json:"variant"`
	}
	if !decode(w, r, &req) {
		return
	}
	b, err := s.bcStore().GetBroadcast(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !broadcast.HasWinnerStep(b.AB) {
		writeErr(w, http.StatusBadRequest, "this broadcast has no A/B winner step")
		return
	}
	found := false
	for _, v := range b.AB.Variants {
		found = found || v.Label == req.Variant
	}
	if !found {
		writeErr(w, http.StatusBadRequest, "choose one of the variants")
		return
	}
	if err := s.bcStore().PickWinner(r.Context(), tenant, id, run, req.Variant); err != nil {
		if errors.Is(err, pg.ErrNotFound) {
			writeErr(w, http.StatusConflict, "this run's winner has already been sent, or its test hasn't finished sending")
			return
		}
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"winner": req.Variant})
}

// ReportAnalytics are the ClickHouse report queries.
type ReportAnalytics interface {
	MessageReport(ctx context.Context, sc ch.Scope, conversionEvent string, conversionDays int) (ch.Report, error)
	FunnelLevels(ctx context.Context, q segment.Query, steps int) ([]uint64, error)
	EventNames(ctx context.Context, tenant int64) ([]string, error)
}

func (s *Server) reports(w http.ResponseWriter) (ReportAnalytics, bool) {
	ra, ok := s.Analytics.(ReportAnalytics)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "reports need the analytics database")
	}
	return ra, ok
}

// broadcastReport: outcomes, engagement and conversions of a broadcast, for every run or one
// (?run=<unix seconds of its scheduled time>), split by A/B variant.
func (s *Server) broadcastReport(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ra, ok := s.reports(w)
	if !ok {
		return
	}
	b, err := s.bcStore().GetBroadcast(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	var run int64
	if v := r.URL.Query().Get("run"); v != "" {
		if run, err = strconv.ParseInt(v, 10, 64); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid run")
			return
		}
	}
	sc := ch.Scope{Tenant: tenant, Source: "broadcast", SourceID: id, Run: run}
	rep, err := ra.MessageReport(r.Context(), sc, b.ConversionEvent, b.ConversionDays)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.messageRevenue(r.Context(), &rep, sc, b.ConversionDays, b.AB != nil); err != nil {
		s.fail(w, err)
		return
	}
	rep.Funnel = ch.BuildFunnel(rep)
	runs, err := s.bcStore().BroadcastRuns(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	rep.Funnel.Audience = audienceAtSend(runs, run)
	writeJSON(w, http.StatusOK, rep)
}

// audienceAtSend sums the audience snapshots of a broadcast's runs (one run when run is set).
func audienceAtSend(runs []model.BroadcastRun, run int64) *ch.AudienceAtSend {
	var a ch.AudienceAtSend
	a.Ready = true
	for _, r := range runs {
		if len(r.Audience) == 0 || (run != 0 && r.ScheduledAt.Unix() != run) {
			continue
		}
		var snap broadcast.AudienceSnapshot
		if json.Unmarshal(r.Audience, &snap) != nil {
			continue
		}
		a.Audience += snap.Audience
		a.AfterExclude += snap.AfterExclude
		a.AfterConsent += snap.AfterConsent
		a.Reachable += snap.Reachable
		a.Ready = a.Ready && snap.Ready
		a.Runs++
	}
	if a.Runs == 0 {
		return nil
	}
	return &a
}

func (s *Server) scheduleBroadcast(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if s.fourEyes(w, r, p) {
		return
	}
	s.launchBroadcast(w, r, p.TenantID, id)
}

// launchBroadcast schedules a broadcast (its own Schedule button, or an approval).
func (s *Server) launchBroadcast(w http.ResponseWriter, r *http.Request, tenant, id int64) bool {
	out, code, err := s.scheduleBroadcastNow(r.Context(), tenant, id)
	if err != nil {
		if code == 0 {
			s.fail(w, err)
		} else {
			writeErr(w, code, err.Error())
		}
		return false
	}
	writeJSON(w, http.StatusOK, out)
	return true
}

// scheduleBroadcastNow schedules a broadcast; code is the HTTP status of a refusal (0: a server error).
func (s *Server) scheduleBroadcastNow(ctx context.Context, tenant, id int64) (map[string]any, int, error) {
	b, err := s.bcStore().GetBroadcast(ctx, tenant, id)
	if err != nil {
		if errors.Is(err, pg.ErrNotFound) {
			return nil, http.StatusNotFound, errors.New("not found")
		}
		return nil, 0, err
	}
	if b.Status == "testing" || b.Status == "running" {
		return nil, http.StatusConflict, errors.New("this broadcast is already sending")
	}
	if err := s.validBroadcast(ctx, b); err != nil {
		return nil, http.StatusBadRequest, err
	}
	first, ok := broadcast.FirstRun(b, s.now())
	if !ok {
		return nil, http.StatusBadRequest, errors.New("the schedule has already ended; move the end later or remove it")
	}
	if err := s.segmentsLiveFor(ctx, tenant, broadcastSegmentIDs(b), first); err != nil {
		if errors.Is(err, errSegmentNotLive) {
			return nil, http.StatusConflict, err
		}
		return nil, 0, err
	}
	if err := s.bcStore().SetBroadcastSchedule(ctx, tenant, id, "scheduled", &first); err != nil {
		return nil, 0, err
	}
	return map[string]any{"id": id, "status": "scheduled", "next_run_at": first}, 0, nil
}

func (s *Server) cancelBroadcast(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.bcStore().SetBroadcastSchedule(r.Context(), tenant, id, "cancelled", nil); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "cancelled"})
}

func (s *Server) deleteBroadcast(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.bcStore().DeleteBroadcast(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sampleContact is used for previews when no contact is chosen.
var sampleContact = model.Contact{ExternalID: "sample", Email: "asha@example.com",
	Attrs: map[string]any{"first_name": "Asha", "name": "Asha Rao", "city": "Pune", "phone": "+919800000001"}}

// previewTemplate renders a template, saved or not, for a real contact (or a sample one), exactly as
// the dispatcher would. event is a sample of the trigger event's properties for {{event.x}}
// (GET /v1/templates/sample-event finds one); campaign names the journey or broadcast.
// Previewing for a real contact shows that contact's data, so it needs contacts view access as well.
func (s *Server) previewTemplate(w http.ResponseWriter, r *http.Request, p model.Principal) {
	tenant := p.TenantID
	var req struct {
		Template   templateReq    `json:"template"`
		ExternalID string         `json:"external_id"`
		Event      map[string]any `json:"event"`
		Campaign   string         `json:"campaign"`
	}
	if !decode(w, r, &req) {
		return
	}
	c := sampleContact
	if req.ExternalID != "" {
		if !p.Perms.Allows(auth.AreaContacts, auth.View) {
			writeErr(w, http.StatusForbidden, "previewing for a contact needs view access to contacts")
			return
		}
		var err error
		if c, err = s.Store.ContactByExternalID(r.Context(), tenant, req.ExternalID); err != nil {
			s.fail(w, err)
			return
		}
	}
	if !eventFits(w, req.Event) {
		return
	}
	t, locale := channel.ForLocale(req.Template.template(tenant), channel.ContactLocale(c)) // the contact's language
	if err := channel.PrepareAMP(&t); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	camp := personalize.Campaign{Type: "test", Name: req.Campaign}
	if camp.Name == "" {
		camp.Name = "Preview"
	}
	links := channel.Links{}
	if t.Channel == channel.Email {
		links = channel.Links{Unsubscribe: "https://example.com/u/preview", Preferences: "https://example.com/p/preview"}
		if s.Tracking != nil {
			links = channel.Links{Unsubscribe: s.Tracking.Base() + "/u/preview", Preferences: s.Tracking.Base() + "/p/preview"}
		}
	}
	out, err := channel.RenderWith(t, c, channel.Vars{Event: req.Event, Campaign: camp, Links: links, Blocks: s.renderBlocks(r.Context(), tenant)})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	resp := map[string]any{"subject": out.Subject, "html": out.HTML, "content": out.Content, "missing": nonNil(out.Missing), "locale": locale,
		"skipped": t.MissingValues == channel.MissingSkip && len(out.Missing) > 0,
		"contact": map[string]any{"external_id": c.ExternalID, "email": c.Email, "phone": c.Attrs["phone"]}}
	if t.Channel == channel.Email {
		// The plain-text part as sent (links stay direct; the unsubscribe link is added at send time
		// when the text doesn't place {{.UnsubscribeURL}}), and the files attached.
		resp["text"], resp["text_generated"] = out.Content.Text, out.TextGenerated
		if out.Content.AMP != "" {
			// The AMP part as personalised, and a script-free look at it (forms can't be sent).
			resp["amp"] = out.Content.AMP
			resp["amp_preview"] = amp.PreviewHTML(amp.Fill(out.Content.AMP, func(string, string) string { return "#" }))
		}
		files := []map[string]any{}
		if ids := out.Content.Attachments; len(ids) > 0 {
			as, err := s.assetStore().AssetsByIDs(r.Context(), tenant, ids)
			if err != nil {
				s.fail(w, err)
				return
			}
			var total int64
			for _, a := range as {
				total += a.Size
				files = append(files, map[string]any{"id": a.ID, "title": a.Title, "filename": a.Filename,
					"content_type": a.ContentType, "size": a.Size})
			}
			if len(as) != len(ids) {
				resp["attachment_error"] = "an attachment doesn't exist in this workspace any more"
			} else if total > assets.MaxAttachmentBytes {
				resp["attachment_error"] = "attachments can be at most 10 MB together"
			}
		}
		resp["attachments"] = files
	}
	writeJSON(w, http.StatusOK, resp)
}

// testProvider sends a fixed test message straight through one provider and reports the provider's
// answer, so admins can check credentials before anything depends on them.
func (s *Server) testProvider(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		To           string `json:"to"`
		TemplateName string `json:"template_name"` // WhatsApp: an approved template (default Meta's hello_world)
	}
	if !decode(w, r, &req) {
		return
	}
	cfg, err := s.chStore().GetProvider(r.Context(), p.TenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	_, sealed, err := s.chStore().ProviderForWebhook(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	secrets, err := dispatch.OpenSecrets(s.Box, sealed)
	if err != nil {
		s.fail(w, err)
		return
	}
	deps := s.providerDeps(p.TenantID, id)
	deps.Sandbox = sandboxSink(s)
	prov, err := providers.New(cfg.Kind, deps, cfg.Config, secrets)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "these settings don't work: "+err.Error())
		return
	}
	if strings.TrimSpace(req.To) == "" {
		writeErr(w, http.StatusBadRequest, "enter where to send the test")
		return
	}
	const text = "Test message from Pulse. If you can read this, the channel is connected."
	rendered := channel.Rendered{Subject: "Pulse test message", HTML: "<p>" + text + "</p>",
		Content: channel.Content{Text: text, Title: "Pulse test", Body: text, TemplateName: req.TemplateName, Language: "en_US"}}
	if cfg.Channel == channel.WhatsApp && rendered.Content.TemplateName == "" {
		rendered.Content.TemplateName = "hello_world"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := prov.Send(ctx, providers.Outbound{TenantID: p.TenantID, MessageID: "test-" + auth.NewToken("")[:12],
		Channel: cfg.Channel, To: strings.TrimSpace(req.To), Platform: "android", From: "Pulse <noreply@pulse.local>", Rendered: rendered})
	if err != nil {
		kind := "temporary problem: retry later"
		if errors.Is(err, providers.ErrUnreachable) {
			kind = "the recipient can't be reached on this channel"
		} else if errors.Is(err, providers.ErrPermanent) {
			kind = "rejected by the provider"
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error(), "meaning": kind})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider_message_id": res.ProviderMessageID})
}

func sandboxSink(s *Server) providers.SandboxSink {
	sink, _ := s.Store.(providers.SandboxSink)
	return sink
}
