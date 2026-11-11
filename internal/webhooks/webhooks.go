// Package webhooks posts workspace events to the customer's URLs (outbound webhooks).
//
// The worker reads enriched events and segment membership changes. Each event a webhook
// subscribes to ("purchase", "email_opened", "segment_entered", or "*") is POSTed as JSON, signed:
//
//	X-Pulse-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256(secret, "<t>.<body>")>
//
// A delivery that fails (network error, timeout, non-2xx) is retried with backoff for about 7
// hours; every attempt is recorded for the webhook's page. Deliveries for one event happen in
// parallel and in no particular order across events, so receivers should use the event id
// (X-Pulse-Delivery) to deduplicate and the event's ts to order.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/event"
	"pulse/internal/model"
)

const (
	retryKey    = "webhook:retry"
	cacheTTL    = 10 * time.Second
	parallel    = 32
	maxRespRead = 1 << 10
)

// Backoff is the wait before each retry; after the last, the delivery is given up.
var Backoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour}

// Webhook is a subscription with its secret opened. REST hook subscriptions (Zapier, Make) belong
// to an integration and may narrow segment events to one segment and form_submitted to one form.
type Webhook struct {
	ID            int64
	TenantID      int64
	URL           string
	Events        []string
	Secret        string
	IntegrationID int64
	SegmentID     int64
	FormID        int64
}

// ContactCreated is the event a webhook receives when a contact becomes known: a new identified
// contact, or an anonymous visitor who identified (the moment date_identified is set).
const ContactCreated = "contact_created"

func (w Webhook) wants(p Payload) bool {
	for _, e := range w.Events {
		if e == "*" && p.Event != ContactCreated || e == p.Event {
			switch {
			case w.SegmentID != 0 && (p.Event == "segment_entered" || p.Event == "segment_exited") && p.SegmentID != w.SegmentID:
				return false
			case w.FormID != 0 && p.Event == "form_submitted" && formID(p.Props) != w.FormID:
				return false
			}
			return true
		}
	}
	return false
}

func formID(props map[string]any) int64 {
	switch v := props["form_id"].(type) {
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

type Delivery struct {
	WebhookID  int64
	EventID    string
	EventName  string
	Attempt    int
	Status     int
	Error      string
	DurationMS int
	Final      bool
	Test       bool // a test send from the app, never retried
}

type Store interface {
	ActiveWebhooks(ctx context.Context) ([]Webhook, error)
	RecordDelivery(ctx context.Context, d Delivery) error
}

// Unsubscriber removes a REST hook subscription whose receiver answered 410 Gone (the REST hooks
// convention for "this subscription no longer exists").
type Unsubscriber interface {
	DeleteHookSubscription(ctx context.Context, webhookID int64) error
}

type Worker struct {
	Store  Store
	RDB    redis.Cmdable
	Client *http.Client // must refuse private addresses in production (safehttp)
	Log    *slog.Logger
	Now    func() time.Time

	mu    sync.Mutex
	hooks map[int64][]Webhook // by tenant
	exp   time.Time
}

// Payload is the JSON body of a delivery.
type Payload struct {
	ID        string         `json:"id"`
	Event     string         `json:"event"`
	Ts        time.Time      `json:"ts"`
	Workspace int64          `json:"workspace_id"`
	Contact   PayloadContact `json:"contact"`
	Props     map[string]any `json:"props,omitempty"`
	SegmentID int64          `json:"segment_id,omitempty"` // segment_entered, segment_exited
}

type PayloadContact struct {
	ID         uint32         `json:"id"`
	ExternalID string         `json:"external_id,omitempty"`
	Attrs      map[string]any `json:"attributes,omitempty"` // the profile after this event
}

// Topics are what the worker consumes.
func Topics() []string { return []string{event.TopicEnriched, event.TopicMembership} }

// HandleBatch delivers the batch's events to every matching webhook and waits for the attempts
// (failures go to the retry queue, so the batch never blocks on a broken receiver for long).
func (w *Worker) HandleBatch(ctx context.Context, recs []*kgo.Record) error {
	type job struct {
		hook Webhook
		p    Payload
	}
	var jobs []job
	for _, r := range recs {
		for _, p := range decode(r) {
			hooks, err := w.forTenant(ctx, p.Workspace)
			if err != nil {
				return err
			}
			for _, h := range hooks {
				if h.wants(p) {
					jobs = append(jobs, job{h, p})
				}
			}
		}
	}
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			body, _ := json.Marshal(j.p)
			w.attempt(ctx, j.hook.ID, j.p.ID, j.p.Event, body, 1)
		}()
	}
	wg.Wait()
	return nil
}

// decode turns a record into the payloads it means: the event itself, plus contact_created when
// this event identified the contact.
func decode(r *kgo.Record) []Payload {
	switch r.Topic {
	case event.TopicEnriched:
		var e event.Event
		if json.Unmarshal(r.Value, &e) != nil || e.ContactID == 0 {
			return nil
		}
		contact := PayloadContact{ID: e.ContactID, ExternalID: e.ExternalID, Attrs: e.Profile}
		out := []Payload{{ID: e.ID, Event: e.Name, Ts: e.Ts, Workspace: e.TenantID, Props: e.Props, Contact: contact}}
		if slices.Contains(e.ChangedAttrs, event.AttrDateIdentified) && e.Profile[event.AttrMergedInto] == nil {
			out = append(out, Payload{ID: "created:" + e.ID, Event: ContactCreated, Ts: e.Ts, Workspace: e.TenantID, Contact: contact})
		}
		return out
	case event.TopicMembership:
		var m model.MembershipChange
		if json.Unmarshal(r.Value, &m) != nil {
			return nil
		}
		name := "segment_exited"
		if m.Entered {
			name = "segment_entered"
		}
		id := fmt.Sprintf("seg:%d:%d:%d:%t", m.SegmentID, m.ContactID, m.At.UnixNano(), m.Entered)
		return []Payload{{ID: id, Event: name, Ts: m.At, Workspace: m.TenantID, SegmentID: m.SegmentID,
			Contact: PayloadContact{ID: m.ContactID}}}
	}
	return nil
}

func (w *Worker) forTenant(ctx context.Context, tenant int64) ([]Webhook, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.hooks == nil || w.now().After(w.exp) {
		all, err := w.Store.ActiveWebhooks(ctx)
		if err != nil {
			return nil, err
		}
		w.hooks = map[int64][]Webhook{}
		for _, h := range all {
			w.hooks[h.TenantID] = append(w.hooks[h.TenantID], h)
		}
		w.exp = w.now().Add(cacheTTL)
	}
	return w.hooks[tenant], nil
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Sign returns the X-Pulse-Signature value for body at t.
func Sign(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "."))
	m.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(m.Sum(nil))
}

// Send makes one delivery request and returns the HTTP status (0 without a response).
func Send(ctx context.Context, c *http.Client, h Webhook, eventID, eventName string, body []byte, now time.Time) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Pulse-Webhooks/1")
	req.Header.Set("X-Pulse-Event", eventName)
	req.Header.Set("X-Pulse-Delivery", eventID)
	req.Header.Set("X-Pulse-Signature", Sign(h.Secret, now, body))
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxRespRead))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

type retry struct {
	WebhookID int64           `json:"w"`
	EventID   string          `json:"e"`
	EventName string          `json:"n"`
	Attempt   int             `json:"a"`
	Body      json.RawMessage `json:"b"`
}

// attempt delivers once, records it, and schedules the next try if it failed.
func (w *Worker) attempt(ctx context.Context, hookID int64, eventID, name string, body []byte, n int) {
	hook := w.lookup(ctx, hookID)
	d := Delivery{WebhookID: hookID, EventID: eventID, EventName: name, Attempt: n}
	if hook == nil {
		return // deleted or turned off since
	}
	start := w.now()
	status, err := Send(ctx, w.Client, *hook, eventID, name, body, start)
	d.Status, d.DurationMS = status, int(time.Since(start).Milliseconds())
	if status == http.StatusGone && hook.IntegrationID != 0 {
		if u, ok := w.Store.(Unsubscriber); ok {
			if derr := u.DeleteHookSubscription(context.WithoutCancel(ctx), hookID); derr != nil {
				w.Log.Error("remove gone subscription", "webhook", hookID, "err", derr)
			}
			w.mu.Lock()
			w.exp = time.Time{} // reload: stop sending to it now
			w.mu.Unlock()
			return
		}
	}
	switch {
	case err == nil:
		d.Final = true
	case n > len(Backoff):
		d.Error, d.Final = err.Error()+"; gave up", true
	default:
		d.Error = err.Error()
		b, _ := json.Marshal(retry{hookID, eventID, name, n + 1, body})
		if rerr := w.RDB.ZAdd(ctx, retryKey, redis.Z{Score: float64(w.now().Add(Backoff[n-1]).UnixMilli()), Member: string(b)}).Err(); rerr != nil {
			w.Log.Error("webhook retry", "err", rerr)
		}
	}
	if rerr := w.Store.RecordDelivery(context.WithoutCancel(ctx), d); rerr != nil {
		w.Log.Error("record webhook delivery", "err", rerr)
	}
}

// RunRetries re-sends due retries until ctx is done. ZREM decides which worker owns each.
func (w *Worker) RunRetries(ctx context.Context) error {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		due, err := w.RDB.ZRangeByScore(ctx, retryKey, &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(w.now().UnixMilli(), 10), Count: 200}).Result()
		if err == nil {
			for _, raw := range due {
				if n, _ := w.RDB.ZRem(ctx, retryKey, raw).Result(); n != 1 {
					continue
				}
				var rt retry
				if json.Unmarshal([]byte(raw), &rt) != nil {
					continue
				}
				w.attempt(ctx, rt.WebhookID, rt.EventID, rt.EventName, rt.Body, rt.Attempt)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// lookup finds an active webhook by id, loading the cache when it's stale (so a deleted or
// paused webhook stops retrying within seconds).
func (w *Worker) lookup(ctx context.Context, id int64) *Webhook {
	if _, err := w.forTenant(ctx, 0); err != nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, hs := range w.hooks {
		for i := range hs {
			if hs[i].ID == id {
				h := hs[i]
				return &h
			}
		}
	}
	return nil
}
