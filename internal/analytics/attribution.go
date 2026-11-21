package analytics

// Revenue attribution: which broadcasts, journeys and UTM campaigns the workspace's revenue (its
// purchase event's amount property) comes from.
//
// A touch is a contact opening or clicking a broadcast or journey message (message_events), or a
// page view carrying a UTM campaign (the web SDK puts the session's campaign on page_view and
// landing_page_viewed). A purchase can be credited to touches by the same contact in the N days up
// to it. Messages and UTM campaigns are attributed separately, each with the chosen model:
//
//   - last_touch: the latest touch gets all of it;
//   - first_touch: the earliest touch in the window gets all of it;
//   - linear: every distinct source touched in the window gets an equal share.
//
// ClickHouse summarizes each attributed purchase (first touch, last touch, distinct sources) and
// the models are applied here, so the arithmetic is the same code the unit tests check.

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"pulse/internal/attr"
	"pulse/internal/event"
	"pulse/internal/segment"
)

// RevenueSettings name the workspace's revenue event and the property holding its amount.
type RevenueSettings struct {
	Event      string `json:"event"`
	AmountProp string `json:"amount_prop"`
	Currency   string `json:"currency"` // ISO 4217 code for display, e.g. INR; amounts are not converted
}

func DefaultRevenue() RevenueSettings {
	return RevenueSettings{Event: "purchase", AmountProp: "amount"}
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

func (r RevenueSettings) Validate() error {
	if !event.ValidName(r.Event) {
		return errors.New("choose the event that records a purchase, e.g. purchase")
	}
	if !attr.ValidKey(r.AmountProp) {
		return errors.New("enter the property that holds the amount, e.g. amount")
	}
	if r.Currency != "" && !currencyRe.MatchString(r.Currency) {
		return errors.New("the currency is a 3-letter code like INR or USD")
	}
	return nil
}

const (
	LastTouch  = "last_touch"
	FirstTouch = "first_touch"
	Linear     = "linear"

	TouchMessages = "messages" // broadcast and journey opens and clicks
	TouchUTM      = "utm"      // page views from UTM-tagged links

	MaxAttributionWindow = 90 // days
)

// ValidModel reports whether m is an attribution model.
func ValidModel(m string) bool { return m == LastTouch || m == FirstTouch || m == Linear }

// TouchKey identifies what gets credit: a broadcast or journey (Kind, ID), or a UTM campaign
// (Kind "utm", Source / Medium / Campaign).
type TouchKey struct {
	Kind     string `json:"kind"` // broadcast | journey | utm
	ID       int64  `json:"id,omitempty"`
	Source   string `json:"source,omitempty"`
	Medium   string `json:"medium,omitempty"`
	Campaign string `json:"campaign,omitempty"`
}

// Touch is one touch of a contact.
type Touch struct {
	Key TouchKey
	At  time.Time
}

// Purchase is one revenue event and the touches before it that may get credit (ClickHouse
// returns their summary: the first and last touch in the window and the distinct sources).
type Purchase struct {
	Amount float64
	First  TouchKey
	Last   TouchKey
	Keys   []TouchKey
}

// Summarize reduces a purchase's touches to what the models need: those in [at-window, at], the
// earliest, the latest and the distinct sources. ok is false when no touch is in the window.
// (ClickHouse computes the same summary with argMin, argMax and groupUniqArray.)
func Summarize(amount float64, at time.Time, window time.Duration, touches []Touch) (Purchase, bool) {
	p := Purchase{Amount: amount}
	var first, last time.Time
	seen := map[TouchKey]bool{}
	for _, t := range touches {
		if t.At.After(at) || t.At.Before(at.Add(-window)) {
			continue
		}
		if first.IsZero() || t.At.Before(first) {
			first, p.First = t.At, t.Key
		}
		if last.IsZero() || !t.At.Before(last) {
			last, p.Last = t.At, t.Key
		}
		if !seen[t.Key] {
			seen[t.Key] = true
			p.Keys = append(p.Keys, t.Key)
		}
	}
	return p, len(p.Keys) > 0
}

// Credit is what one source earned.
type Credit struct {
	TouchKey
	Name        string  `json:"name,omitempty"` // broadcast or journey name, filled in by the API
	Revenue     float64 `json:"revenue"`
	Conversions float64 `json:"conversions"` // purchases credited (fractions with the linear model)
}

// Attribute applies a model to attributed purchases and returns each source's credit, highest
// revenue first. Revenue and conversions add up to the attributed purchases' totals.
func Attribute(model string, purchases []Purchase) []Credit {
	by := map[TouchKey]*Credit{}
	add := func(k TouchKey, rev, conv float64) {
		c := by[k]
		if c == nil {
			c = &Credit{TouchKey: k}
			by[k] = c
		}
		c.Revenue += rev
		c.Conversions += conv
	}
	for _, p := range purchases {
		switch model {
		case FirstTouch:
			add(p.First, p.Amount, 1)
		case Linear:
			keys := dedupe(p.Keys)
			share := 1 / float64(len(keys))
			for _, k := range keys {
				add(k, p.Amount*share, share)
			}
		default:
			add(p.Last, p.Amount, 1)
		}
	}
	out := make([]Credit, 0, len(by))
	for _, c := range by {
		c.Revenue = round2(c.Revenue)
		c.Conversions = math.Round(c.Conversions*1000) / 1000
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Revenue != out[j].Revenue {
			return out[i].Revenue > out[j].Revenue
		}
		if out[i].Conversions != out[j].Conversions {
			return out[i].Conversions > out[j].Conversions
		}
		return keyString(out[i].TouchKey) < keyString(out[j].TouchKey)
	})
	return out
}

func dedupe(keys []TouchKey) []TouchKey {
	seen := map[TouchKey]bool{}
	out := keys[:0:0]
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// Touch keys travel through ClickHouse as one string: "broadcast|12", "journey|3",
// "utm|source\x1fmedium\x1fcampaign".
func keyString(k TouchKey) string {
	if k.Kind == "utm" {
		return "utm|" + k.Source + "\x1f" + k.Medium + "\x1f" + k.Campaign
	}
	return k.Kind + "|" + strconv.FormatInt(k.ID, 10)
}

// ParseTouchKey decodes a key string from an attribution query.
func ParseTouchKey(s string) (TouchKey, error) {
	kind, rest, ok := strings.Cut(s, "|")
	if !ok {
		return TouchKey{}, fmt.Errorf("bad touch key %q", s)
	}
	if kind == "utm" {
		parts := strings.SplitN(rest, "\x1f", 3)
		if len(parts) != 3 {
			return TouchKey{}, fmt.Errorf("bad touch key %q", s)
		}
		return TouchKey{Kind: kind, Source: parts[0], Medium: parts[1], Campaign: parts[2]}, nil
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || (kind != "broadcast" && kind != "journey") {
		return TouchKey{}, fmt.Errorf("bad touch key %q", s)
	}
	return TouchKey{Kind: kind, ID: id}, nil
}

// purchases is (id, contact_id, ts, amount) for each revenue event in [from, to), one row per
// event id (the pipeline may deliver an event twice before ClickHouse merges the copies).
func purchases(t uint64, rs RevenueSettings, from, to time.Time) (string, []any) {
	amount, aargs := segment.PropValueSQL(rs.AmountProp)
	args := append(aargs, t, rs.Event, from, to)
	// Aliases differ from the columns: ClickHouse resolves aliases in WHERE.
	return "SELECT id, any(contact_id) AS buyer, any(ts) AS at, any(toFloat64OrZero(" + amount + ")) AS amount FROM events" +
		" WHERE tenant_id = ? AND name = ? AND ts >= ? AND ts < ? GROUP BY id", args
}

// PurchasesSQL is one row per revenue event in [from, to): (id, buyer, at, amount).
func PurchasesSQL(tenantID int64, rs RevenueSettings, from, to time.Time) (string, []any) {
	return purchases(uint64(tenantID), rs, from.UTC(), to.UTC())
}

// RevenueTotalsSQL: (orders, revenue, buyers) of revenue events in [from, to).
func RevenueTotalsSQL(tenantID int64, rs RevenueSettings, from, to time.Time) Query {
	p, args := purchases(uint64(tenantID), rs, from.UTC(), to.UTC())
	return Query{SQL: "SELECT count(), sum(amount), uniqExact(buyer) FROM (" + p + ")", Args: args}
}

// AttributionSQL returns one row per purchase in [from, to) with at least one touch of the given
// family in the windowDays before it: (amount, first touch key, last touch key, distinct keys).
func AttributionSQL(tenantID int64, rs RevenueSettings, family string, windowDays int, from, to time.Time) (Query, error) {
	if err := rs.Validate(); err != nil {
		return Query{}, err
	}
	if err := CheckRange(from, to); err != nil {
		return Query{}, err
	}
	if windowDays < 1 || windowDays > MaxAttributionWindow {
		return Query{}, fmt.Errorf("the attribution window is 1 to %d days", MaxAttributionWindow)
	}
	t := uint64(tenantID)
	from, to = from.UTC(), to.UTC()
	touchFrom := from.AddDate(0, 0, -windowDays)
	var touches string
	var targs []any
	switch family {
	case TouchMessages:
		// message_events is sorted by (tenant, source, source_id, kind): two sources, two kinds.
		touches = "SELECT contact_id, ts, concat(source, '|', toString(source_id)) AS k FROM message_events" +
			" WHERE tenant_id = ? AND source IN ('broadcast', 'journey') AND kind IN ('opened', 'clicked') AND ts >= ? AND ts < ?"
		targs = []any{t, touchFrom, to}
	case TouchUTM:
		touches = "SELECT contact_id, ts, concat('utm|', JSONExtractString(props, 'utm_source'), char(31)," +
			" JSONExtractString(props, 'utm_medium'), char(31), JSONExtractString(props, 'utm_campaign')) AS k FROM events" +
			" WHERE tenant_id = ? AND name IN (?, ?) AND ts >= ? AND ts < ? AND JSONExtractString(props, 'utm_campaign') != ''"
		targs = []any{t, "page_view", event.LandingPageViewed, touchFrom, to}
	default:
		return Query{}, fmt.Errorf("unknown touch family %q", family)
	}
	p, pargs := purchases(t, rs, from, to)
	args := append(pargs, targs...)
	args = append(args, windowDays)
	return Query{
		SQL: "SELECT p.amount, argMin(tc.k, tc.ts), argMax(tc.k, tc.ts), groupUniqArray(50)(tc.k)" +
			" FROM (" + p + ") AS p INNER JOIN (" + touches + ") AS tc ON tc.contact_id = p.buyer" +
			" WHERE tc.ts <= p.at AND tc.ts >= p.at - toIntervalDay(?)" +
			" GROUP BY p.id, p.amount",
		Args: args,
	}, nil
}

// RecipientRevenueSQL: revenue from a broadcast's or journey's recipients — revenue events a
// recipient did within days after their first message — grouped by key (an expression over
// message_events: "”" for one total, "variant" per A/B variant). scope is a WHERE over
// message_events with its arguments. Rows: (key, revenue, orders, buyers).
func RecipientRevenueSQL(tenantID int64, rs RevenueSettings, scope string, scopeArgs []any, key string, days int) Query {
	amount, aargs := segment.PropValueSQL(rs.AmountProp)
	t := uint64(tenantID)
	args := append([]any{}, scopeArgs...)
	args = append(args, aargs...)
	args = append(args, t, rs.Event)
	args = append(args, scopeArgs...)
	args = append(args, days)
	return Query{
		SQL: "SELECT m.k, sum(p.amount), count(), uniqExact(p.buyer)" +
			" FROM (SELECT contact_id, " + key + " AS k, min(ts) AS sent_at FROM message_events WHERE " + scope + " AND kind = 'sent' GROUP BY contact_id, k) AS m" +
			" INNER JOIN (SELECT id, any(contact_id) AS buyer, any(ts) AS at, any(toFloat64OrZero(" + amount + ")) AS amount FROM events" +
			" WHERE tenant_id = ? AND name = ? AND ts >= (SELECT min(ts) FROM message_events WHERE " + scope + " AND kind = 'sent') GROUP BY id) AS p" +
			" ON p.buyer = m.contact_id" +
			" WHERE p.at >= m.sent_at AND p.at < m.sent_at + toIntervalDay(?)" +
			" GROUP BY m.k",
		Args: args,
	}
}
