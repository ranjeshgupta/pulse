package ch

// M4, delivery truth: failure reasons of a broadcast or journey, the campaigns compared, and a
// contact's message history and activity.

import (
	"context"
	"sort"
	"strconv"
	"time"

	"pulse/internal/model"
	"pulse/internal/reasons"
	"pulse/internal/segment"
)

// DownloadURL matches click destinations that are files: Pulse assets (/a/{key}) and links ending
// in a document, archive or media extension. Such clicks also count as downloads.
const DownloadURL = `(?i)(/a/[A-Za-z0-9_-]+(/download)?([?#]|$)|\.(pdf|zip|docx?|xlsx?|pptx?|csv|ics|apk|epub|mp3|mp4)([?#]|$))`

// outcomeKinds are the outcomes with a reason.
const reasonKinds = "('failed', 'bounced', 'complained', 'suppressed', 'capped')"

// ReasonCount is how many contacts' messages ended for one reason.
type ReasonCount struct {
	Kind     string `json:"kind"` // failed | bounced | complained | suppressed | capped
	Code     string `json:"code"`
	Label    string `json:"label"`
	Group    string `json:"group,omitempty"` // skipped: consent, reachability, cap, missing_value, deadline
	Contacts uint64 `json:"contacts"`
}

func reasonRow(kind, code string, n uint64) ReasonCount {
	if code == "" {
		switch kind {
		case "bounced":
			code = reasons.SoftBounce // linked bounces always have a code; this keeps old rows readable
		case "complained":
			code = reasons.Complaint
		case "capped":
			code = reasons.Capped
		case "suppressed":
			code = reasons.Consent
		default:
			code = reasons.Unknown
		}
	}
	return ReasonCount{Kind: kind, Code: code, Label: reasons.Label(code), Group: reasons.SkipGroup(code), Contacts: n}
}

func sortReasons(rs []ReasonCount) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Contacts != rs[j].Contacts {
			return rs[i].Contacts > rs[j].Contacts
		}
		return rs[i].Code < rs[j].Code
	})
}

// reasons fills the report's reasons (and per step for journeys) and the targeted count.
func (s *Store) reasons(ctx context.Context, sc Scope, rep *Report) error {
	w, args := sc.Where()
	if err := s.conn.QueryRow(ctx, "SELECT uniqExact(contact_id) FROM message_events WHERE "+w+
		" AND kind IN ('sent', 'failed', 'suppressed', 'capped', 'fallback', 'bounced')", args...).Scan(&rep.Targeted); err != nil {
		return err
	}
	rows, err := s.conn.Query(ctx, "SELECT node, kind, reason_code, uniqExact(contact_id) FROM message_events WHERE "+w+
		" AND kind IN "+reasonKinds+" GROUP BY node, kind, reason_code", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	rep.Reasons = []ReasonCount{}
	total := map[[2]string]uint64{}
	byNode := map[string][]ReasonCount{}
	for rows.Next() {
		var node, kind, code string
		var n uint64
		if err := rows.Scan(&node, &kind, &code, &n); err != nil {
			return err
		}
		r := reasonRow(kind, code, n)
		total[[2]string{r.Kind, r.Code}] += n // per-step counts add up: a contact gets one message per step
		if sc.Source == "journey" && node != "" {
			byNode[node] = append(byNode[node], r)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for k, n := range total {
		rep.Reasons = append(rep.Reasons, reasonRow(k[0], k[1], n))
	}
	sortReasons(rep.Reasons)
	if len(byNode) > 0 {
		for _, rs := range byNode {
			sortReasons(rs)
		}
		rep.ReasonsByNode = byNode
	}
	return nil
}

// CampaignStats is one broadcast's or journey's outcomes in a period (the Campaigns dashboard).
type CampaignStats struct {
	Counts  model.MessageCounts `json:"counts"`
	Reasons []ReasonCount       `json:"reasons"`
	Revenue float64             `json:"revenue"`
	Buyers  uint64              `json:"buyers"` // recipients who did the revenue event within the window
}

// CampaignCompare returns the outcomes of every broadcast and journey with messages in [from, to),
// keyed "broadcast:<id>" / "journey:<id>". Revenue: revenue events of each recipient within
// windowDays of their first message of that campaign in the period.
func (s *Store) CampaignCompare(ctx context.Context, tenant int64, from, to time.Time, revenueEvent, amountProp string, windowDays int) (map[string]*CampaignStats, error) {
	out := map[string]*CampaignStats{}
	get := func(src string, id uint64) *CampaignStats {
		k := src + ":" + strconv.FormatUint(id, 10)
		if out[k] == nil {
			out[k] = &CampaignStats{Reasons: []ReasonCount{}}
		}
		return out[k]
	}
	const scope = "tenant_id = ? AND source IN ('broadcast', 'journey') AND ts >= ? AND ts < ?"
	rows, err := s.conn.Query(ctx, `SELECT source, source_id, kk, uniqExact(contact_id) FROM message_events
		ARRAY JOIN if(kind = 'clicked' AND match(url, ?), ['clicked', 'downloaded'], [kind]) AS kk
		WHERE `+scope+` GROUP BY source, source_id, kk`, DownloadURL, uint64(tenant), from, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var src, kind string
		var id, n uint64
		if err := rows.Scan(&src, &id, &kind, &n); err != nil {
			rows.Close()
			return nil, err
		}
		addCount(&get(src, id).Counts, kind, n)
	}
	rows.Close()
	rrows, err := s.conn.Query(ctx, `SELECT source, source_id, kind, reason_code, uniqExact(contact_id) FROM message_events
		WHERE `+scope+` AND kind IN `+reasonKinds+` GROUP BY source, source_id, kind, reason_code`, uint64(tenant), from, to)
	if err != nil {
		return nil, err
	}
	for rrows.Next() {
		var src, kind, code string
		var id, n uint64
		if err := rrows.Scan(&src, &id, &kind, &code, &n); err != nil {
			rrows.Close()
			return nil, err
		}
		c := get(src, id)
		c.Reasons = append(c.Reasons, reasonRow(kind, code, n))
	}
	rrows.Close()
	for _, c := range out {
		sortReasons(c.Reasons)
	}
	if revenueEvent == "" || windowDays <= 0 {
		return out, nil
	}
	amount, aargs := segment.PropValueSQL(amountProp)
	args := []any{uint64(tenant), from, to}
	args = append(args, aargs...)
	args = append(args, uint64(tenant), revenueEvent, from, to.AddDate(0, 0, windowDays), windowDays)
	vrows, err := s.conn.Query(ctx, `
		SELECT m.source, m.source_id, uniqExact(e.contact_id), sum(e.amount)
		FROM (SELECT source, source_id, contact_id, min(ts) AS first FROM message_events
		      WHERE `+scope+` AND kind = 'sent' GROUP BY source, source_id, contact_id) AS m
		INNER JOIN (SELECT id, any(contact_id) AS contact_id, any(ts) AS at, any(toFloat64OrZero(`+amount+`)) AS amount FROM events
		      WHERE tenant_id = ? AND name = ? AND ts >= ? AND ts < ? GROUP BY id) AS e ON e.contact_id = m.contact_id
		WHERE e.at >= m.first AND e.at < m.first + toIntervalDay(?)
		GROUP BY m.source, m.source_id`, args...)
	if err != nil {
		return nil, err
	}
	defer vrows.Close()
	for vrows.Next() {
		var src string
		var id, buyers uint64
		var rev float64
		if err := vrows.Scan(&src, &id, &buyers, &rev); err != nil {
			return nil, err
		}
		c := get(src, id)
		c.Buyers, c.Revenue = buyers, rev
	}
	return out, vrows.Err()
}

// WorkspaceReasons counts contacts per outcome reason over every message of a period (test sends
// excluded), for the Campaigns dashboard's "failures by reason".
func (s *Store) WorkspaceReasons(ctx context.Context, tenant int64, from, to time.Time) ([]ReasonCount, error) {
	rows, err := s.conn.Query(ctx, `SELECT kind, reason_code, uniqExact(contact_id) FROM message_events
		WHERE tenant_id = ? AND source IN ('broadcast', 'journey') AND ts >= ? AND ts < ? AND kind IN `+reasonKinds+`
		GROUP BY kind, reason_code`, uint64(tenant), from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReasonCount{}
	for rows.Next() {
		var kind, code string
		var n uint64
		if err := rows.Scan(&kind, &code, &n); err != nil {
			return nil, err
		}
		out = append(out, reasonRow(kind, code, n))
	}
	sortReasons(out)
	return out, rows.Err()
}

// ContactMessage is one message to a contact with what became of it.
type ContactMessage struct {
	MessageID   string    `json:"message_id"`
	Channel     string    `json:"channel"`
	Source      string    `json:"source"` // broadcast | journey | other
	SourceID    uint64    `json:"source_id"`
	Node        string    `json:"node,omitempty"`
	Variant     string    `json:"variant,omitempty"`
	TemplateID  uint64    `json:"template_id"`
	At          time.Time `json:"at"`   // first outcome
	Last        time.Time `json:"last"` // latest outcome or engagement
	Status      string    `json:"status"`
	Kinds       []string  `json:"kinds"` // every outcome and engagement seen
	ReasonCode  string    `json:"reason_code,omitempty"`
	ReasonLabel string    `json:"reason_label,omitempty"`
	Reason      string    `json:"reason,omitempty"` // the provider's or dispatcher's own words
	Opened      bool      `json:"opened"`
	Clicked     bool      `json:"clicked"`
	Clicks      uint64    `json:"clicks"`
}

// statusRank orders outcomes: a message's status is its best-known final state.
var statusRank = map[string]int{"complained": 11, "bounced": 10, "failed": 9, "clicked": 8, "opened": 7, "read": 6,
	"delivered": 5, "sent": 4, "fallback": 3, "suppressed": 2, "capped": 2, "deferred": 1}

// MessageStatus picks the status a message shows from its outcome kinds: a bounce, complaint or
// failure wins (it is the news), then the furthest engagement.
func MessageStatus(kinds []string) string {
	best, rank := "", 0
	for _, k := range kinds {
		if r := statusRank[k]; r > rank {
			best, rank = k, r
		}
	}
	return best
}

// ContactMessages lists a contact's messages, newest first (at most limit, before `before` when set),
// from the event log so the provider's own words come along.
func (s *Store) ContactMessages(ctx context.Context, tenant int64, contact uint32, before time.Time, limit int) ([]ContactMessage, error) {
	args := []any{uint64(tenant), contact}
	cond := ""
	if !before.IsZero() {
		cond = " AND ts < ?"
		args = append(args, before)
	}
	args = append(args, limit)
	rows, err := s.conn.Query(ctx, `
		SELECT message_id, any(ch), any(bid), any(jid), any(run), any(variant), any(tpl), min(ts) AS at, max(ts),
			groupUniqArray(kind), argMaxIf(code, ts, code != ''), argMaxIf(reason, ts, reason != ''), countIf(kind = 'clicked')
		FROM (
			SELECT JSONExtractString(props, 'message_id') AS message_id,
				splitByChar('_', name)[1] AS ch, splitByChar('_', name)[2] AS kind,
				JSONExtractUInt(props, 'broadcast_id') AS bid, JSONExtractUInt(props, 'journey_id') AS jid,
				JSONExtractUInt(props, 'run') AS run, JSONExtractString(props, 'variant') AS variant,
				JSONExtractUInt(props, 'template_id') AS tpl, ts,
				JSONExtractString(props, 'reason_code') AS code, JSONExtractString(props, 'reason') AS reason
			FROM events
			WHERE tenant_id = ? AND contact_id = ? AND JSONHas(props, 'message_id')
			  AND match(name, '^(email|sms|whatsapp|rcs|push|inbox)_(sent|failed|delivered|read|fallback|opened|clicked|suppressed|capped|deferred|bounced|complained)$')`+cond+`
			LIMIT 1 BY id
		)
		WHERE message_id != '' AND NOT startsWith(message_id, 'test:')
		GROUP BY message_id ORDER BY at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContactMessage{}
	for rows.Next() {
		var m ContactMessage
		var bid, jid, run uint64
		if err := rows.Scan(&m.MessageID, &m.Channel, &bid, &jid, &run, &m.Variant, &m.TemplateID, &m.At, &m.Last,
			&m.Kinds, &m.ReasonCode, &m.Reason, &m.Clicks); err != nil {
			return nil, err
		}
		sort.Strings(m.Kinds)
		switch {
		case bid > 0:
			m.Source, m.SourceID = "broadcast", bid
		case jid > 0:
			m.Source, m.SourceID = "journey", jid
			if parts := splitColon(m.MessageID); len(parts) > 1 {
				m.Node = parts[1]
			}
		default:
			m.Source = "other"
		}
		m.Status = MessageStatus(m.Kinds)
		for _, k := range m.Kinds {
			m.Opened = m.Opened || k == "opened"
			m.Clicked = m.Clicked || k == "clicked"
		}
		if m.ReasonCode == "" && (m.Status == "failed" || m.Status == "suppressed" || m.Status == "capped") {
			m.ReasonCode = reasons.Classify(m.Channel, m.Status, m.Reason)
		}
		if m.ReasonCode != "" {
			m.ReasonLabel = reasons.Label(m.ReasonCode)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func splitColon(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// ActivityCounts are a contact's own events in the last 1, 7, 30 and 365 days (activity as the
// Users dashboard counts it), and their message engagement.
type ActivityCounts struct {
	Events     map[string]uint64 `json:"events"`   // "1", "7", "30", "365"
	Messages   map[string]uint64 `json:"messages"` // messages sent to them
	Opens      map[string]uint64 `json:"opens"`
	Clicks     map[string]uint64 `json:"clicks"`
	TopEvents  []NameCount       `json:"top_events"` // last 30 days
	LastActive *time.Time        `json:"last_active,omitempty"`
}

// ContactActivity counts one contact's activity. activeCond is analytics.ActiveCondition.
func (s *Store) ContactActivity(ctx context.Context, tenant int64, contact uint32, activeCond string, now time.Time) (ActivityCounts, error) {
	a := ActivityCounts{Events: map[string]uint64{}, Messages: map[string]uint64{}, Opens: map[string]uint64{}, Clicks: map[string]uint64{}, TopEvents: []NameCount{}}
	var e1, e7, e30, e365, m1, m7, m30, m365, o1, o7, o30, o365, c1, c7, c30, c365 uint64
	var last time.Time
	err := s.conn.QueryRow(ctx, `
		SELECT
			uniqExactIf(id, act AND ts >= ?), uniqExactIf(id, act AND ts >= ?), uniqExactIf(id, act AND ts >= ?), uniqExactIf(id, act),
			uniqExactIf(id, sent AND ts >= ?), uniqExactIf(id, sent AND ts >= ?), uniqExactIf(id, sent AND ts >= ?), uniqExactIf(id, sent),
			uniqExactIf(mid, op AND ts >= ?), uniqExactIf(mid, op AND ts >= ?), uniqExactIf(mid, op AND ts >= ?), uniqExactIf(mid, op),
			uniqExactIf(id, cl AND ts >= ?), uniqExactIf(id, cl AND ts >= ?), uniqExactIf(id, cl AND ts >= ?), uniqExactIf(id, cl),
			maxIf(ts, act)
		FROM (SELECT id, ts, (`+activeCond+`) AS act, match(name, '^(email|sms|whatsapp|rcs|push|inbox)_sent$') AS sent,
			match(name, '^(email|inbox)_opened$') AS op, match(name, '_clicked$') AS cl, JSONExtractString(props, 'message_id') AS mid
			FROM events WHERE tenant_id = ? AND contact_id = ? AND ts >= ?)`,
		now.AddDate(0, 0, -1), now.AddDate(0, 0, -7), now.AddDate(0, 0, -30),
		now.AddDate(0, 0, -1), now.AddDate(0, 0, -7), now.AddDate(0, 0, -30),
		now.AddDate(0, 0, -1), now.AddDate(0, 0, -7), now.AddDate(0, 0, -30),
		now.AddDate(0, 0, -1), now.AddDate(0, 0, -7), now.AddDate(0, 0, -30),
		uint64(tenant), contact, now.AddDate(0, 0, -365)).
		Scan(&e1, &e7, &e30, &e365, &m1, &m7, &m30, &m365, &o1, &o7, &o30, &o365, &c1, &c7, &c30, &c365, &last)
	if err != nil {
		return a, err
	}
	put := func(m map[string]uint64, v ...uint64) {
		for i, k := range []string{"1", "7", "30", "365"} {
			m[k] = v[i]
		}
	}
	put(a.Events, e1, e7, e30, e365)
	put(a.Messages, m1, m7, m30, m365)
	put(a.Opens, o1, o7, o30, o365)
	put(a.Clicks, c1, c7, c30, c365)
	if !last.IsZero() && last.Year() > 1970 {
		a.LastActive = &last
	}
	rows, err := s.conn.Query(ctx, `SELECT name, uniqExact(id) AS n FROM events WHERE tenant_id = ? AND contact_id = ? AND ts >= ? AND (`+activeCond+`)
		GROUP BY name ORDER BY n DESC, name LIMIT 10`, uint64(tenant), contact, now.AddDate(0, 0, -30))
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var n NameCount
		if err := rows.Scan(&n.Name, &n.Count); err != nil {
			return a, err
		}
		a.TopEvents = append(a.TopEvents, n)
	}
	return a, rows.Err()
}

// ContactPurchase is one revenue event of a contact.
type ContactPurchase struct {
	ID       string    `json:"id"`
	Ts       time.Time `json:"ts"`
	Amount   float64   `json:"amount"`
	Product  string    `json:"product_code,omitempty"`
	Category string    `json:"product_category,omitempty"`
}

// ContactPurchases lists a contact's latest revenue events and their totals.
func (s *Store) ContactPurchases(ctx context.Context, tenant int64, contact uint32, event, amountProp string, limit int) ([]ContactPurchase, uint64, float64, error) {
	amount, aargs := segment.PropValueSQL(amountProp)
	args := append(aargs, uint64(tenant), contact, event)
	rows, err := s.conn.Query(ctx, `SELECT id, ts, toFloat64OrZero(`+amount+`), product_code, product_category FROM events
		WHERE tenant_id = ? AND contact_id = ? AND name = ? ORDER BY ts DESC LIMIT 1 BY id`, args...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	out := []ContactPurchase{}
	var n uint64
	var total float64
	for rows.Next() {
		var p ContactPurchase
		if err := rows.Scan(&p.ID, &p.Ts, &p.Amount, &p.Product, &p.Category); err != nil {
			return nil, 0, 0, err
		}
		n++
		total += p.Amount
		if len(out) < limit {
			out = append(out, p)
		}
	}
	return out, n, total, rows.Err()
}

// ContactEventsFiltered is the contact's timeline with filters: event names (none: all), only the
// contact's own activity (activeCond) or only Pulse's events, before a time; newest first.
func (s *Store) ContactEventsFiltered(ctx context.Context, tenant int64, contact uint32, names []string, cond string, before time.Time, limit int) ([]EventRow, error) {
	q := "SELECT id, name, ts, props FROM events WHERE tenant_id = ? AND contact_id = ?"
	args := []any{uint64(tenant), contact}
	if len(names) > 0 {
		q += " AND has(?, name)"
		args = append(args, names)
	}
	if cond != "" {
		q += " AND (" + cond + ")"
	}
	if !before.IsZero() {
		q += " AND ts < ?"
		args = append(args, before)
	}
	q += " ORDER BY ts DESC LIMIT 1 BY id LIMIT ?"
	args = append(args, limit)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventRow{}
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.ID, &e.Name, &e.Ts, &e.Props); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AudienceAtSend is a broadcast's audience when its runs started (summed over runs).
type AudienceAtSend struct {
	Audience     int64 `json:"audience"`
	AfterExclude int64 `json:"after_exclude"`
	AfterConsent int64 `json:"after_consent"`
	Reachable    int64 `json:"reachable"`
	Runs         int   `json:"runs"` // runs with a snapshot
	Ready        bool  `json:"ready"`
}

// Funnel is a campaign's path from audience to engagement, with what fell out at each step.
type Funnel struct {
	Audience *AudienceAtSend `json:"audience,omitempty"` // broadcasts
	Enrolled *int64          `json:"enrolled,omitempty"` // journeys
	Targeted uint64          `json:"targeted"`           // contacts with any outcome
	// Skipped: not tried, by group (consent, reachability, cap, missing_value, deadline).
	Skipped      map[string]uint64 `json:"skipped"`
	Sent         uint64            `json:"sent"`
	Delivered    uint64            `json:"delivered"`
	Opened       uint64            `json:"opened"`
	Read         uint64            `json:"read"`
	Clicked      uint64            `json:"clicked"`
	Downloaded   uint64            `json:"downloaded"`
	Converted    uint64            `json:"converted"`
	Bounced      uint64            `json:"bounced"`
	HardBounced  uint64            `json:"hard_bounced"`
	SoftBounced  uint64            `json:"soft_bounced"`
	Complained   uint64            `json:"complained"`
	Failed       uint64            `json:"failed"` // tried and failed (skips not included)
	Unsubscribed uint64            `json:"unsubscribed"`
}

// BuildFunnel derives the funnel from a report's counts and reasons.
func BuildFunnel(rep Report) Funnel {
	t := rep.Totals
	f := Funnel{Targeted: rep.Targeted, Skipped: map[string]uint64{"consent": 0, "reachability": 0, "cap": 0, "missing_value": 0, "deadline": 0},
		Sent: t.Sent, Delivered: t.Delivered, Opened: t.Opened, Read: t.Read, Clicked: t.Clicked, Downloaded: t.Downloaded,
		Converted: t.Converted, Bounced: t.Bounced, Complained: t.Complained, Unsubscribed: t.Unsubscribed}
	var skippedFailures uint64
	for _, r := range rep.Reasons {
		if r.Group != "" {
			f.Skipped[r.Group] += r.Contacts
			if r.Kind == "failed" {
				skippedFailures += r.Contacts
			}
		}
		// Bounces and complaints on every channel, by bounce type (an SMS to an invalid number is a
		// hard bounce, a WhatsApp block a complaint), the same the bounce rules and filters use.
		// Reasons the funnel shows as skipped (not on the channel, device gone) aren't counted twice.
		if (r.Kind == "bounced" || r.Kind == "failed" || r.Kind == "complained") && r.Group == "" {
			switch reasons.BounceType(r.Code) {
			case reasons.BounceHard:
				f.HardBounced += r.Contacts
			case reasons.BounceSoft:
				f.SoftBounced += r.Contacts
			case reasons.BounceComplaint:
				if r.Kind != "complained" { // complained messages are in Totals already
					f.Complained += r.Contacts
				}
			}
		}
	}
	if rep.Late > f.Skipped["deadline"] { // deadline drops recorded before reason codes
		skippedFailures += rep.Late - f.Skipped["deadline"]
		f.Skipped["deadline"] = rep.Late
	}
	// Suppressed and capped are their own outcomes; skipped failures come out of Failed.
	if t.Suppressed > f.Skipped["consent"] {
		f.Skipped["consent"] = t.Suppressed
	}
	if t.Capped > f.Skipped["cap"] {
		f.Skipped["cap"] = t.Capped
	}
	if t.Failed > skippedFailures {
		f.Failed = t.Failed - skippedFailures
	}
	if b := f.HardBounced + f.SoftBounced; b > f.Bounced {
		f.Bounced = b // every channel's bounces, not only email's
	}
	return f
}
