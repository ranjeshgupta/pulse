package bounces

// The bounce mailbox: a workspace may point Pulse at an IMAP mailbox (its SMTP Return-Path /
// feedback-loop address). The integrations role checks every enabled mailbox every few minutes:
// it reads unseen messages, parses delivery status notifications and feedback reports
// (ParseReport), records them like bounce webhooks (Events), and marks each message it read as
// \Seen, so it is processed once. Messages that aren't reports are marked seen too and counted as
// skipped. The password is sealed with the server key and only ever shown masked.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/consent"
	"pulse/internal/event"
	"pulse/internal/kafka"
	"pulse/internal/model"
	"pulse/internal/tracking"
)

// TLS modes of a mailbox connection.
const (
	TLSImplicit = "tls"      // TLS from the start (port 993)
	TLSStart    = "starttls" // plain connection upgraded with STARTTLS (port 143)
	TLSNone     = "none"     // no encryption: development only
)

const (
	// CheckEvery is how often an enabled mailbox is read.
	CheckEvery = 5 * time.Minute
	// MaxPerCheck is the most messages one check reads; the rest wait for the next check.
	MaxPerCheck = 200
)

// Mailbox is a workspace's bounce mailbox settings. The password is stored sealed (not here).
type Mailbox struct {
	TenantID    int64      `json:"-"`
	Enabled     bool       `json:"enabled"`
	Host        string     `json:"host"`
	Port        int        `json:"port"`
	TLS         string     `json:"tls"`
	Username    string     `json:"username"`
	Folder      string     `json:"folder"`
	HasPassword bool       `json:"has_password"`
	LastCheckAt *time.Time `json:"last_check_at,omitempty"`
	LastError   string     `json:"last_error"`
	Processed   int64      `json:"processed"` // bounces and complaints recorded, all time
	Skipped     int64      `json:"skipped"`   // messages that weren't reports
}

// Validate checks the settings; allowPlain allows TLSNone (local development only).
func (m *Mailbox) Validate(allowPlain bool) error {
	m.Host = strings.TrimSpace(m.Host)
	m.Username = strings.TrimSpace(m.Username)
	m.Folder = strings.TrimSpace(m.Folder)
	if m.Folder == "" {
		m.Folder = "INBOX"
	}
	if m.TLS == "" {
		m.TLS = TLSImplicit
	}
	if m.Port == 0 {
		m.Port = map[string]int{TLSImplicit: 993, TLSStart: 143, TLSNone: 143}[m.TLS]
	}
	switch {
	case m.TLS != TLSImplicit && m.TLS != TLSStart && m.TLS != TLSNone:
		return errors.New("tls must be tls, starttls or none")
	case m.TLS == TLSNone && !allowPlain:
		return errors.New("connect with TLS or STARTTLS: an unencrypted connection would send the password in clear text")
	case m.Host == "" || len(m.Host) > 253 || strings.ContainsAny(m.Host, " /:@"):
		return errors.New("host must be a server name such as imap.example.com")
	case m.Port < 1 || m.Port > 65535:
		return errors.New("port must be between 1 and 65535")
	case m.Username == "" || len(m.Username) > 320:
		return errors.New("username is required")
	case len(m.Folder) > 200 || strings.ContainsAny(m.Folder, "\r\n\"*%"):
		return errors.New("folder must be a mailbox name such as INBOX or Bounces")
	}
	return nil
}

// SameServer reports whether two settings talk to the same server as the same user with at least
// the same protection; when they don't, a stored password must not be reused (a settings editor
// could otherwise send it to a server of their own).
func (m Mailbox) SameServer(o Mailbox) bool {
	return strings.EqualFold(m.Host, o.Host) && m.Port == o.Port && m.Username == o.Username && m.TLS == o.TLS
}

// Message is one message read from the mailbox.
type Message struct {
	UID uint32
	Raw []byte
}

// Session is an open, selected mailbox.
type Session interface {
	// Unseen returns up to max unseen messages, oldest first, without marking them seen.
	Unseen(ctx context.Context, max int) ([]Message, error)
	// MarkSeen flags the messages \Seen.
	MarkSeen(ctx context.Context, uids []uint32) error
	Close() error
}

// Opener connects, signs in and selects the folder.
type Opener func(ctx context.Context, m Mailbox, password string) (Session, error)

// MailboxStore is what the poller needs from Postgres.
type MailboxStore interface {
	// ClaimDueMailboxes returns enabled mailboxes due for a check, with their sealed passwords,
	// and moves their next check forward (FOR UPDATE SKIP LOCKED, so workers share them).
	ClaimDueMailboxes(ctx context.Context, limit int, every time.Duration) ([]Mailbox, [][]byte, error)
	FinishMailboxCheck(ctx context.Context, tenant int64, processed, skipped int, lastErr string) error
	ExternalIDByEmail(ctx context.Context, tenant int64, email string) (string, error)
	ContactByID(ctx context.Context, tenant int64, id uint32) (model.Contact, error)
}

// Publisher records events (into events.raw).
type Publisher interface {
	PublishEvents(ctx context.Context, tenant int64, evs []event.Event) error
}

// Poller checks the due mailboxes.
type Poller struct {
	Store  MailboxStore
	Open   Opener
	Unseal func([]byte) (string, error)
	Pub    Publisher
	Log    *slog.Logger
	// Refs checks the message references bounce emails quote (nil: they are recorded unlinked).
	Refs RefParser
}

// Run checks due mailboxes every tick until ctx ends.
func (p *Poller) Run(ctx context.Context, tick time.Duration) error {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		p.checkDue(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (p *Poller) checkDue(ctx context.Context) {
	boxes, sealed, err := p.Store.ClaimDueMailboxes(ctx, 20, CheckEvery)
	if err != nil {
		p.Log.Error("bounce mailboxes", "err", err)
		return
	}
	for i, m := range boxes {
		processed, skipped, err := p.Check(ctx, m, sealed[i])
		msg := ""
		if err != nil {
			msg = err.Error()
			p.Log.Warn("bounce mailbox check", "tenant", m.TenantID, "err", err)
		}
		if err := p.Store.FinishMailboxCheck(ctx, m.TenantID, processed, skipped, msg); err != nil {
			p.Log.Error("bounce mailbox status", "tenant", m.TenantID, "err", err)
		}
	}
}

// Check reads one mailbox once: the bounces and complaints recorded, and the messages skipped.
func (p *Poller) Check(ctx context.Context, m Mailbox, sealedPassword []byte) (processed, skipped int, err error) {
	if len(sealedPassword) == 0 {
		return 0, 0, errors.New("no password saved: enter the mailbox password")
	}
	pw, err := p.Unseal(sealedPassword)
	if err != nil {
		return 0, 0, errors.New("the saved password can't be read (was the server key changed?): enter it again")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	s, err := p.Open(ctx, m, pw)
	if err != nil {
		return 0, 0, err
	}
	defer s.Close()
	msgs, err := s.Unseen(ctx, MaxPerCheck)
	if err != nil {
		return 0, 0, fmt.Errorf("read messages: %w", err)
	}
	if len(msgs) == 0 {
		return 0, 0, nil
	}
	var all []Bounce
	for _, msg := range msgs {
		bs, err := ParseReport(msg.Raw)
		if err != nil || len(bs) == 0 {
			skipped++
			continue
		}
		sum := sha256.Sum256(msg.Raw)
		for i := range bs {
			bs[i].Key = fmt.Sprintf("mbx:%x:%d", sum[:12], i)
		}
		all = append(all, bs...)
	}
	evs, err := Events(all, Link(ctx, m.TenantID, p.Refs, p.Store))
	if err != nil {
		return 0, 0, err
	}
	if len(evs) > 0 {
		// Publish before marking: if publishing fails the messages stay unseen and are read again.
		if err := p.Pub.PublishEvents(ctx, m.TenantID, evs); err != nil {
			return 0, 0, fmt.Errorf("record bounces: %w", err)
		}
	}
	uids := make([]uint32, len(msgs))
	for i, msg := range msgs {
		uids[i] = msg.UID
	}
	if err := s.MarkSeen(ctx, uids); err != nil {
		return len(all), skipped, fmt.Errorf("mark messages read: %w", err)
	}
	return len(all), skipped, nil
}

// AttrEmailBounced marks a contact whose address hard-bounced: email to them is suppressed.
const AttrEmailBounced = consent.AttrEmailBounced

// Target is the contact a report is recorded on.
type Target struct {
	ExternalID string
	// Props are the ids of the message the report is about (message_id, template_id, journey_id or
	// broadcast_id, run, variant), when it quoted a valid reference.
	Props map[string]any
	// SameAddress: the contact's address is still the one the report is about (a hard bounce of an
	// address the contact has since replaced doesn't mark the new one).
	SameAddress bool
}

// Resolver finds a report's contact; false skips the report.
type Resolver func(b Bounce) (Target, bool)

// reasonCodes of the reports (package reasons).
var reasonCodes = map[string]string{Hard: "hard_bounce", Soft: "soft_bounce", Complaint: "complaint"}

// Events turns reports into events on their contacts (resolve finds them; unknown addresses are
// skipped): a hard bounce records email_bounced and marks the address bounced (no more email); a
// soft bounce only records email_bounced; a complaint records email_complained and unsubscribes
// the contact from email; a delivery report records email_delivered (linked reports only).
// Linked reports carry the message's ids and get ids derived from the message, so a webhook the
// service repeats records one event.
func Events(bs []Bounce, resolve Resolver) ([]event.Event, error) {
	var evs []event.Event
	for _, b := range bs {
		t, ok := resolve(b)
		if !ok || t.ExternalID == "" {
			continue
		}
		mid, _ := t.Props["message_id"].(string)
		if b.Kind == Delivered && mid == "" {
			continue
		}
		props := map[string]any{"kind": b.Kind, "reason": clip(b.Reason), "source": b.Source, "email": b.Email}
		if c := reasonCodes[b.Kind]; c != "" {
			props["reason_code"] = c
		}
		for k, v := range t.Props {
			props[k] = v
		}
		id := func(suffix string) string {
			switch {
			case mid != "":
				return "fb:" + mid + ":" + b.Kind + suffix
			case b.Key != "":
				return b.Key + suffix
			}
			return ""
		}
		switch b.Kind {
		case Hard:
			ev := event.Event{ID: id(":b"), ExternalID: t.ExternalID, Name: "email_bounced", Props: props}
			if t.SameAddress {
				ev.Attrs = map[string]any{AttrEmailBounced: true}
			}
			evs = append(evs, ev)
		case Soft:
			evs = append(evs, event.Event{ID: id(":b"), ExternalID: t.ExternalID, Name: "email_bounced", Props: props})
		case Complaint:
			evs = append(evs, event.Event{ID: id(":c"), ExternalID: t.ExternalID, Name: "email_complained", Props: props},
				event.Event{ID: id(":u"), ExternalID: t.ExternalID, Name: consent.Unsubscribed, Props: map[string]any{"channel": "email", "source": "complaint"}})
		case Delivered:
			delete(props, "kind")
			evs = append(evs, event.Event{ID: id(""), ExternalID: t.ExternalID, Name: "email_delivered", Props: props})
		}
	}
	return evs, nil
}

// RefParser checks message references (tracking.Codec).
type RefParser interface {
	ParseRef(s string) (tracking.Ref, error)
}

// Contacts looks contacts up for Link.
type Contacts interface {
	ExternalIDByEmail(ctx context.Context, tenant int64, email string) (string, error)
	ContactByID(ctx context.Context, tenant int64, id uint32) (model.Contact, error)
}

// Link resolves a report of a workspace: a reference signed by this server for this workspace
// names the message and its contact (the tenant in a report body is never trusted on its own);
// otherwise the contact with the address. refs may be nil (no server key: nothing is linked).
func Link(ctx context.Context, tenant int64, refs RefParser, cs Contacts) Resolver {
	return func(b Bounce) (Target, bool) {
		if b.Ref != "" && refs != nil {
			if r, err := refs.ParseRef(b.Ref); err == nil && r.TenantID == tenant {
				if c, err := cs.ContactByID(ctx, tenant, r.ContactID); err == nil && c.ExternalID != "" {
					return Target{ExternalID: c.ExternalID, Props: r.Props(),
						SameAddress: b.Email == "" || strings.EqualFold(c.Email, b.Email)}, true
				}
			}
		}
		if b.Kind == Delivered || b.Email == "" {
			return Target{}, false
		}
		ext, err := cs.ExternalIDByEmail(ctx, tenant, b.Email)
		if err != nil || ext == "" {
			return Target{}, false
		}
		return Target{ExternalID: ext, SameAddress: true}, true
	}
}

// KafkaPublisher records events into events.raw.
type KafkaPublisher struct{ Pub kafka.Publisher }

func (k KafkaPublisher) PublishEvents(ctx context.Context, tenant int64, evs []event.Event) error {
	recs := make([]*kgo.Record, 0, len(evs))
	for i := range evs {
		evs[i].TenantID = tenant
		if err := evs[i].Normalize(time.Now()); err != nil {
			return err
		}
		r, err := kafka.JSON(event.TopicRaw, evs[i].RawKey(), &evs[i])
		if err != nil {
			return err
		}
		recs = append(recs, r)
	}
	return k.Pub.Publish(ctx, recs...)
}
