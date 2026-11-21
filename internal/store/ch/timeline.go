package ch

// Campaign timelines (batch O5): a broadcast's or journey's activity by day or hour in a time zone
// — message outcomes (optionally of one journey step), journey entries and finishes, and the
// trigger segment's joins and leaves. Counts are unique contacts per bucket.

import (
	"context"
	"time"
)

// TimelineQuery selects a campaign's activity in [From, To), bucketed in Zone.
type TimelineQuery struct {
	Tenant   int64
	Source   string // broadcast | journey
	SourceID int64
	Step     string // journeys: one step's messages only
	Segment  int64  // the trigger segment (0: none)
	From, To time.Time
	Hourly   bool
	Zone     string
}

// Timeline is a campaign's activity over time.
type Timeline struct {
	Messages  []BucketCount `json:"messages"`            // kinds: sent, delivered, failed, bounced, opened, clicked, read…
	Lifecycle []BucketCount `json:"lifecycle,omitempty"` // journeys: entered, completed
	Segment   []BucketCount `json:"segment,omitempty"`   // joined, left
}

// bucketExpr is the bucket of a ts column in the zone (first argument), as text.
func (q TimelineQuery) bucketExpr(col string) (string, []any) {
	if q.Hourly {
		return "formatDateTime(toStartOfHour(" + col + ", ?), '%Y-%m-%d %H:00', ?)", []any{q.Zone, q.Zone}
	}
	return "toString(toDate(" + col + ", ?))", []any{q.Zone}
}

func (s *Store) bucketRows(ctx context.Context, sql string, args []any) ([]BucketCount, error) {
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BucketCount{}
	for rows.Next() {
		var b BucketCount
		if err := rows.Scan(&b.T, &b.Kind, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CampaignTimeline builds a campaign's timeline.
func (s *Store) CampaignTimeline(ctx context.Context, q TimelineQuery) (Timeline, error) {
	var tl Timeline
	from, to := q.From.UTC(), q.To.UTC()
	b, bargs := q.bucketExpr("ts")
	where := "tenant_id = ? AND source = ? AND source_id = ? AND ts >= ? AND ts < ?"
	args := []any{uint64(q.Tenant), q.Source, uint64(q.SourceID), from, to}
	if q.Step != "" {
		where += " AND node = ?"
		args = append(args, q.Step)
	}
	var err error
	if tl.Messages, err = s.bucketRows(ctx, "SELECT "+b+" AS b, kind, uniqExact(contact_id) FROM message_events WHERE "+where+
		" AND kind IN "+seriesKinds+" GROUP BY b, kind ORDER BY b", append(append([]any{}, bargs...), args...)); err != nil {
		return tl, err
	}
	if q.Source == "journey" {
		if tl.Lifecycle, err = s.bucketRows(ctx, "SELECT "+b+" AS b, if(name = 'journey_entered', 'entered', 'completed') AS k, uniqExact(contact_id)"+
			" FROM events WHERE tenant_id = ? AND name IN ('journey_entered', 'journey_completed') AND ts >= ? AND ts < ?"+
			" AND JSONExtractUInt(props, 'journey_id') = ? GROUP BY b, k ORDER BY b",
			append(append([]any{}, bargs...), uint64(q.Tenant), from, to, uint64(q.SourceID))); err != nil {
			return tl, err
		}
	}
	if q.Segment != 0 {
		if tl.Segment, err = s.bucketRows(ctx, "SELECT "+b+" AS b, if(entered = 1, 'joined', 'left') AS k, uniqExact(contact_id)"+
			" FROM segment_changes WHERE tenant_id = ? AND segment_id = ? AND ts >= ? AND ts < ? GROUP BY b, k ORDER BY b",
			append(append([]any{}, bargs...), uint64(q.Tenant), uint64(q.Segment), from, to)); err != nil {
			return tl, err
		}
	}
	return tl, nil
}
