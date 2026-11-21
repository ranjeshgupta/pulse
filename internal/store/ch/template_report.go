package ch

// Template reports (batch O4): one message template's outcomes across every campaign that sent it
// (one-time campaigns, journey steps, tests and API sends), over a chosen period: totals, a series
// by day or hour, an opens/clicks heatmap by weekday and hour, and splits by campaign, channel and
// provider, with failure reasons and top links. Counts are unique contacts, as everywhere else.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"pulse/internal/model"
)

// TemplateQuery selects a template's messages in [From, To), bucketed in Zone.
type TemplateQuery struct {
	Tenant   int64
	Template int64
	From, To time.Time
	Hourly   bool   // the series by hour instead of by day
	Zone     string // IANA zone for buckets and the heatmap (validated by the caller)
}

func (q TemplateQuery) where() (string, []any) {
	return "tenant_id = ? AND template_id = ? AND ts >= ? AND ts < ?",
		[]any{uint64(q.Tenant), uint64(q.Template), q.From.UTC(), q.To.UTC()}
}

// BucketCount is one point of a series: contacts with an outcome kind in a bucket.
type BucketCount struct {
	T     string `json:"t"` // bucket start, "2026-11-21" or "2026-11-21 14:00", in the zone
	Kind  string `json:"kind"`
	Count uint64 `json:"count"`
}

// CampaignCounts are the outcomes of the template in one campaign.
type CampaignCounts struct {
	Source   string              `json:"source"` // broadcast | journey | other (tests, API)
	SourceID int64               `json:"source_id"`
	Counts   model.MessageCounts `json:"counts"`
}

// TemplateReport is everything the template's Report tab shows.
type TemplateReport struct {
	Totals     model.MessageCounts            `json:"totals"`
	Series     []BucketCount                  `json:"series"`
	Heatmap    [7][24]uint64                  `json:"heatmap"` // [Monday..Sunday][hour]: opens and clicks
	ByCampaign []CampaignCounts               `json:"by_campaign"`
	ByChannel  map[string]model.MessageCounts `json:"by_channel"`
	ByProvider map[string]model.MessageCounts `json:"by_provider"` // provider id ("0": unknown, before O4)
	Reasons    []ReasonCount                  `json:"reasons"`
	Links      []LinkCount                    `json:"links"`
}

// seriesKinds are the outcomes drawn on the template's chart.
var seriesKinds = "('sent', 'delivered', 'failed', 'bounced', 'complained', 'opened', 'clicked', 'read', 'unsubscribed')"

// TemplateReport builds a template's report.
func (s *Store) TemplateReport(ctx context.Context, q TemplateQuery) (TemplateReport, error) {
	rep := TemplateReport{Series: []BucketCount{}, ByCampaign: []CampaignCounts{}, Reasons: []ReasonCount{}, Links: []LinkCount{},
		ByChannel: map[string]model.MessageCounts{}, ByProvider: map[string]model.MessageCounts{}}
	w, args := q.where()

	groups := func(key string) (map[string]model.MessageCounts, error) {
		rows, err := s.conn.Query(ctx, "SELECT "+key+" AS k, kind, uniqExact(contact_id) FROM message_events WHERE "+w+" GROUP BY k, kind", args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := map[string]model.MessageCounts{}
		for rows.Next() {
			var k, kind string
			var n uint64
			if err := rows.Scan(&k, &kind, &n); err != nil {
				return nil, err
			}
			c := out[k]
			addCount(&c, kind, n)
			out[k] = c
		}
		return out, rows.Err()
	}
	totals, err := groups("''")
	if err != nil {
		return rep, err
	}
	rep.Totals = totals[""]
	if rep.ByChannel, err = groups("channel"); err != nil {
		return rep, err
	}
	if rep.ByProvider, err = groups("toString(provider_id)"); err != nil {
		return rep, err
	}
	camps, err := groups("concat(source, ':', toString(source_id))")
	if err != nil {
		return rep, err
	}
	for k, c := range camps {
		src, idText, found := strings.Cut(k, ":")
		id, err := strconv.ParseInt(idText, 10, 64)
		if !found || err != nil {
			continue
		}
		rep.ByCampaign = append(rep.ByCampaign, CampaignCounts{Source: src, SourceID: id, Counts: c})
	}

	// The series, bucketed in the workspace's zone.
	bucket := "toString(toDate(ts, ?))"
	if q.Hourly {
		bucket = "formatDateTime(toStartOfHour(ts, ?), '%Y-%m-%d %H:00', ?)"
	}
	bargs := []any{q.Zone}
	if q.Hourly {
		bargs = append(bargs, q.Zone)
	}
	rows, err := s.conn.Query(ctx, "SELECT "+bucket+" AS b, kind, uniqExact(contact_id) FROM message_events WHERE "+w+
		" AND kind IN "+seriesKinds+" GROUP BY b, kind ORDER BY b", append(bargs, args...)...)
	if err != nil {
		return rep, err
	}
	for rows.Next() {
		var b BucketCount
		if err := rows.Scan(&b.T, &b.Kind, &b.Count); err != nil {
			rows.Close()
			return rep, err
		}
		rep.Series = append(rep.Series, b)
	}
	rows.Close()

	// When people engage: opens and clicks (events, not people) by weekday and hour.
	hrows, err := s.conn.Query(ctx, "SELECT toDayOfWeek(ts, 0, ?) AS d, toHour(ts, ?) AS h, count() FROM message_events WHERE "+w+
		" AND kind IN ('opened', 'clicked', 'read') GROUP BY d, h", append([]any{q.Zone, q.Zone}, args...)...)
	if err != nil {
		return rep, err
	}
	for hrows.Next() {
		var d, h uint8
		var n uint64
		if err := hrows.Scan(&d, &h, &n); err != nil {
			hrows.Close()
			return rep, err
		}
		if d >= 1 && d <= 7 && h < 24 {
			rep.Heatmap[d-1][h] = n
		}
	}
	hrows.Close()

	rrows, err := s.conn.Query(ctx, "SELECT kind, reason_code, uniqExact(contact_id) AS n FROM message_events WHERE "+w+
		" AND kind IN "+reasonKinds+" GROUP BY kind, reason_code ORDER BY n DESC LIMIT 20", args...)
	if err != nil {
		return rep, err
	}
	for rrows.Next() {
		var kind, code string
		var n uint64
		if err := rrows.Scan(&kind, &code, &n); err != nil {
			rrows.Close()
			return rep, err
		}
		rep.Reasons = append(rep.Reasons, reasonRow(kind, code, n))
	}
	rrows.Close()
	sortReasons(rep.Reasons)

	lrows, err := s.conn.Query(ctx, "SELECT url, uniqExact(contact_id) AS c, uniqExact(id) FROM message_events WHERE "+w+
		" AND kind = 'clicked' AND url != '' GROUP BY url ORDER BY c DESC LIMIT 10", args...)
	if err != nil {
		return rep, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var l LinkCount
		if err := lrows.Scan(&l.URL, &l.Contacts, &l.Clicks); err != nil {
			return rep, err
		}
		rep.Links = append(rep.Links, l)
	}
	return rep, lrows.Err()
}
