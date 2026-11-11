package integrations

// The service runs CRM pulls on a schedule and pushes contacts on demand. Pulled records become
// crm_synced events (props integration_id, source, remote_id) carrying the mapped attributes and
// the record's id (hubspot_id, salesforce_id), so the profile role applies them in order with the
// contact's other events, and reserved attributes stay untouched.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
)

// Events Pulse records for CRM connectors.
const (
	EventCRMSynced  = "crm_synced" // a pulled record: attrs carry the mapped fields
	EventCRMPushed  = "crm_pushed" // a contact was pushed: props integration_id, remote_id
	EventPushFailed = "integration_push_failed"
)

// ErrNotFound is returned by stores for a connector that doesn't exist in the workspace.
var ErrNotFound = errors.New("not found")

// Store is what the service reads and writes (Postgres in production).
type Store interface {
	GetIntegration(ctx context.Context, tenant, id int64) (Connector, error)
	// ClaimDuePulls returns connectors whose pull is due and moves their next pull forward.
	ClaimDuePulls(ctx context.Context, limit int) ([]Connector, error)
	// FinishPull records a pull's outcome; again schedules the next one right away (more records wait).
	FinishPull(ctx context.Context, id int64, cursor, status, lastErr string, again bool) error
	SetIntegrationStatus(ctx context.Context, id int64, status, lastErr string) error
	LogIntegration(ctx context.Context, id int64, e LogEntry) error
	// ExternalIDByEmail finds the contact with this email ("" when there is none).
	ExternalIDByEmail(ctx context.Context, tenant int64, email string) (string, error)
}

// Service pulls and pushes CRM contacts.
type Service struct {
	Store Store
	// Open opens a connector's sealed secrets (nil: no server key, so nothing can connect).
	Open func(sealed []byte) (map[string]string, error)
	HTTP *http.Client // must refuse private addresses in production (safehttp)
	Pub  kafka.Publisher
	Log  *slog.Logger
	Now  func() time.Time
}

const (
	pullLimit = 1000 // records per pull; more are pulled right after
	pushTries = 3
)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Load returns a connector of the workspace with its secrets opened.
func (s *Service) Load(ctx context.Context, tenant, id int64) (Connector, error) {
	c, err := s.Store.GetIntegration(ctx, tenant, id)
	if err != nil {
		return c, err
	}
	return c, s.OpenSecrets(&c)
}

// OpenSecrets fills c.Secrets from c.Sealed.
func (s *Service) OpenSecrets(c *Connector) error {
	c.Secrets = map[string]string{}
	if len(c.Sealed) == 0 {
		return nil
	}
	if s.Open == nil {
		return errors.New("the server has no secret key, so stored credentials can't be used")
	}
	sec, err := s.Open(c.Sealed)
	if err != nil {
		return fmt.Errorf("open stored credentials: %w", err)
	}
	c.Secrets = sec
	return nil
}

func (s *Service) log(ctx context.Context, id int64, action string, count int, err error, okMsg string) {
	e := LogEntry{Action: action, OK: err == nil, Count: count, Message: okMsg}
	if err != nil {
		e.Message = err.Error()
	}
	if lerr := s.Store.LogIntegration(context.WithoutCancel(ctx), id, e); lerr != nil && s.Log != nil {
		s.Log.Error("integration log", "integration", id, "err", lerr)
	}
}

// ---- pulls ----

// cursor is "<RFC 3339 time> <id>,<id>": the newest modification time pulled and the records
// already pulled at exactly that time (so they aren't pulled again).
func parseCursor(c string) (time.Time, map[string]bool) {
	ts, ids, _ := strings.Cut(c, " ")
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t = time.Unix(0, 0).UTC()
	}
	seen := map[string]bool{}
	for _, id := range strings.Split(ids, ",") {
		if id != "" {
			seen[id] = true
		}
	}
	return t, seen
}

func formatCursor(t time.Time, ids []string) string {
	if len(ids) > 200 {
		ids = ids[len(ids)-200:]
	}
	return t.UTC().Format(time.RFC3339Nano) + " " + strings.Join(ids, ",")
}

// externalID picks the contact a record belongs to: the contact with its email, else a new one
// named by the email, else by the remote id.
func (s *Service) externalID(ctx context.Context, tenant int64, kind, email, remoteID string) (string, error) {
	if email != "" {
		ext, err := s.Store.ExternalIDByEmail(ctx, tenant, email)
		if err != nil || ext != "" {
			return ext, err
		}
		return email, nil
	}
	return kind + "-" + remoteID, nil
}

// Pull pulls the records changed since the connector's cursor and records them. It returns how
// many were recorded and whether more are waiting.
func (s *Service) Pull(ctx context.Context, c Connector) (int, string, bool, error) {
	k, _ := Lookup(c.Kind)
	crm, err := NewCRM(c, s.HTTP)
	if err != nil {
		return 0, c.Cursor, false, err
	}
	since, seen := parseCursor(c.Cursor)
	fields := remoteFields(c.Mapping)
	recs, more, err := crm.Pull(ctx, since, fields, pullLimit)
	emailField := emailRemote(c.Mapping)
	newest, atNewest := since, []string{}
	for id := range seen {
		atNewest = append(atNewest, id)
	}
	slices.Sort(atNewest)
	var evs []event.Event
	for _, r := range recs {
		if r.Modified.Equal(since) && seen[r.RemoteID] {
			continue
		}
		attrs := pullAttrs(c.Mapping, r.Fields)
		email := strings.TrimSpace(attr.String(r.Fields[emailField]))
		if NormalizeEmail(email) == "" {
			email = ""
			delete(attrs, "email")
		}
		ext, xerr := s.externalID(ctx, c.TenantID, c.Kind, email, r.RemoteID)
		if xerr != nil {
			return 0, c.Cursor, false, xerr
		}
		attrs[k.RemoteID] = r.RemoteID
		evs = append(evs, event.Event{
			ID:       "crm:" + Sha256Hex(fmt.Sprintf("%d|%s|%s", c.ID, r.RemoteID, r.Modified.UTC().Format(time.RFC3339Nano)))[:40],
			TenantID: c.TenantID, ExternalID: ext, Name: EventCRMSynced, Ts: s.now().UTC(),
			Attrs: attrs, Email: email,
			Props: map[string]any{"integration_id": c.ID, "source": c.Kind, "remote_id": r.RemoteID},
		})
		switch {
		case r.Modified.After(newest):
			newest, atNewest = r.Modified, []string{r.RemoteID}
		case r.Modified.Equal(newest):
			atNewest = append(atNewest, r.RemoteID)
		}
	}
	if perr := s.publish(ctx, evs); perr != nil {
		return 0, c.Cursor, false, perr
	}
	cursor := c.Cursor
	if len(recs) > 0 {
		cursor = formatCursor(newest, atNewest)
	}
	return len(evs), cursor, more && err == nil, err
}

func (s *Service) publish(ctx context.Context, evs []event.Event) error {
	if len(evs) == 0 {
		return nil
	}
	recs := make([]*kgo.Record, 0, len(evs))
	for i := range evs {
		if err := evs[i].Normalize(s.now()); err != nil {
			return err
		}
		r, err := kafka.JSON(event.TopicRaw, evs[i].RawKey(), &evs[i])
		if err != nil {
			return err
		}
		recs = append(recs, r)
	}
	return s.Pub.Publish(ctx, recs...)
}

// RunPull pulls one connector now and records the outcome.
func (s *Service) RunPull(ctx context.Context, c Connector) error {
	if err := s.OpenSecrets(&c); err != nil {
		_ = s.Store.FinishPull(ctx, c.ID, c.Cursor, "error", err.Error(), false)
		s.log(ctx, c.ID, "pull", 0, err, "")
		return err
	}
	n, cursor, more, err := s.Pull(ctx, c)
	status, msg := "ok", ""
	if err != nil {
		status, msg = "error", err.Error()
	}
	if ferr := s.Store.FinishPull(ctx, c.ID, cursor, status, msg, more); ferr != nil {
		return ferr
	}
	okMsg := fmt.Sprintf("%d contacts updated", n)
	if more {
		okMsg += "; more to pull"
	}
	if err != nil || n > 0 || c.Cursor == "" {
		s.log(ctx, c.ID, "pull", n, err, okMsg)
	}
	return err
}

// RunPulls pulls due connectors until ctx is done.
func (s *Service) RunPulls(ctx context.Context, every time.Duration) error {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		due, err := s.Store.ClaimDuePulls(ctx, 5)
		if err != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.Error("integration pulls", "err", err)
		}
		for _, c := range due {
			pctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			if err := s.RunPull(pctx, c); err != nil && s.Log != nil {
				s.Log.Warn("integration pull failed", "integration", c.ID, "kind", c.Kind, "err", err)
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// ---- pushes ----

// PushContact sends a contact to a CRM connector through its mapping, then records crm_pushed
// with the record's id (kept as hubspot_id / salesforce_id for the next push). eventID makes the
// recording idempotent (a journey step key). It retries briefly on network errors, 429 and 5xx.
func (s *Service) PushContact(ctx context.Context, tenant, id int64, c model.Contact, eventID string) (string, error) {
	conn, err := s.Load(ctx, tenant, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("integration %d doesn't exist", id)
		}
		return "", err
	}
	k, _ := Lookup(conn.Kind)
	if !k.Push {
		return "", fmt.Errorf("%s can't receive contacts", k.Name)
	}
	if !conn.Active {
		return "", fmt.Errorf("the %s integration %q is turned off", k.Name, conn.Name)
	}
	crm, err := NewCRM(conn, s.HTTP)
	if err != nil {
		return "", err
	}
	known := attr.String(c.Attrs[k.RemoteID])
	fields := PushFields(conn.Mapping, c.Email, c.Attrs)
	var remote string
	for try := 0; try < pushTries; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(try) * 500 * time.Millisecond):
			}
		}
		remote, err = crm.Push(ctx, known, c.Email, fields)
		if err == nil || !Retryable(err) {
			break
		}
	}
	s.log(ctx, conn.ID, "push", 1, err, fmt.Sprintf("%s → %s %s", c.ExternalID, k.Name, remote))
	if err != nil {
		_ = s.Store.SetIntegrationStatus(ctx, conn.ID, "error", "push: "+err.Error())
		return "", err
	}
	_ = s.Store.SetIntegrationStatus(ctx, conn.ID, "ok", "")
	e := event.Event{ID: eventID, TenantID: tenant, ExternalID: c.ExternalID, Name: EventCRMPushed, Ts: s.now().UTC(),
		Props: map[string]any{"integration_id": conn.ID, "source": conn.Kind, "remote_id": remote}}
	if remote != "" && remote != known {
		e.Attrs = map[string]any{k.RemoteID: remote}
	}
	if eventID == "" {
		e.ID = event.NewID()
	} else if len(eventID) > 64 {
		e.ID = "crmp:" + Sha256Hex(eventID)[:40]
	}
	return remote, s.publish(ctx, []event.Event{e})
}

// Test checks a connector's credentials against its platform.
func (s *Service) Test(ctx context.Context, c Connector) error {
	k, _ := Lookup(c.Kind)
	var err error
	switch {
	case k.Pull || k.Push:
		var crm CRM
		if crm, err = NewCRM(c, s.HTTP); err == nil {
			err = crm.Test(ctx)
		}
	case k.Audiences:
		var p Platform
		if p, err = NewPlatform(c, s.HTTP); err == nil {
			err = p.Test(ctx)
		}
	case c.Kind == KindShopify:
		err = CheckShopify(ctx, c, s.HTTP)
	default:
		return errors.New("nothing to test: this integration doesn't call out")
	}
	s.log(ctx, c.ID, "test", 0, err, "connection works")
	if c.ID != 0 {
		if err != nil {
			_ = s.Store.SetIntegrationStatus(ctx, c.ID, "error", "test: "+err.Error())
		} else if c.Status != "ok" {
			_ = s.Store.SetIntegrationStatus(ctx, c.ID, "ok", "")
		}
	}
	return err
}
