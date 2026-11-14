package pg

// The Overview page's summary (GET /v1/stats/summary): contacts added lately and the one-time
// campaigns sending now, coming up and just sent. Every query reads an index range of one
// workspace (contacts_created and broadcasts_tenant from 028_overview.sql, broadcasts_due), never
// the whole contacts table.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// NewContactCounts are the contacts added today, in the last 7 days (today included) and in the 7
// days before those. Merged-away duplicates don't count, as in Counts.
type NewContactCounts struct {
	Today    int64 `json:"today"`
	Week     int64 `json:"week"`
	PrevWeek int64 `json:"prev_week"`
}

// NewContacts counts contacts created since today, weekFrom and prevFrom (prevFrom < weekFrom <=
// today). It reads the partial index contacts_created: only the last 14 days' entries.
func (s *Store) NewContacts(ctx context.Context, tenant int64, today, weekFrom, prevFrom time.Time) (NewContactCounts, error) {
	var c NewContactCounts
	err := s.db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE created_at >= $2),
		       count(*) FILTER (WHERE created_at >= $3),
		       count(*) FILTER (WHERE created_at < $3)
		FROM contacts
		WHERE tenant_id = $1 AND created_at >= $4 AND NOT (attrs ? 'merged_into')`, tenant, today, weekFrom, prevFrom).
		Scan(&c.Today, &c.Week, &c.PrevWeek)
	return c, err
}

// BroadcastBrief is a one-time campaign on the Overview. At is when its next run starts (coming
// up), or when its current or latest run started (sending, just sent); Run is that run's key in
// message_events (unix seconds of its scheduled time; 0 for coming up).
type BroadcastBrief struct {
	ID      int64     `json:"id"`
	Name    string    `json:"name"`
	Channel string    `json:"channel"`
	Status  string    `json:"status"`
	Repeat  string    `json:"repeat"`
	At      time.Time `json:"at"`
	Run     int64     `json:"run,omitempty"`
}

// OverviewBroadcasts are the one-time campaigns the Overview lists.
type OverviewBroadcasts struct {
	Sending []BroadcastBrief `json:"sending"` // running, or an A/B test waiting for its winner
	Next    []BroadcastBrief `json:"next"`    // scheduled, soonest first
	Recent  []BroadcastBrief `json:"recent"`  // latest finished run since `since`, newest first
}

// OverviewBroadcasts lists at most limit broadcasts of each kind.
func (s *Store) OverviewBroadcasts(ctx context.Context, tenant int64, since time.Time, limit int) (OverviewBroadcasts, error) {
	out := OverviewBroadcasts{Sending: []BroadcastBrief{}, Next: []BroadcastBrief{}, Recent: []BroadcastBrief{}}
	scan := func(dst *[]BroadcastBrief, sql string, args ...any) error {
		rows, err := s.db.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (BroadcastBrief, error) {
			var b BroadcastBrief
			var scheduled *time.Time
			err := r.Scan(&b.ID, &b.Name, &b.Channel, &b.Status, &b.Repeat, &b.At, &scheduled)
			if scheduled != nil {
				b.Run = scheduled.Unix()
			}
			return b, err
		})
		*dst = append(*dst, got...)
		return err
	}
	if err := scan(&out.Sending, `
		SELECT b.id, b.name, b.channel, b.status, b.repeat, coalesce(r.started_at, b.updated_at), r.scheduled_at
		FROM broadcasts b
		LEFT JOIN LATERAL (SELECT scheduled_at, started_at FROM broadcast_runs WHERE broadcast_id = b.id
		                   ORDER BY scheduled_at DESC LIMIT 1) r ON true
		WHERE b.tenant_id = $1 AND b.status IN ('running', 'testing')
		ORDER BY b.updated_at DESC LIMIT $2`, tenant, limit); err != nil {
		return out, err
	}
	if err := scan(&out.Next, `
		SELECT id, name, channel, status, repeat, next_run_at, NULL::timestamptz
		FROM broadcasts
		WHERE tenant_id = $1 AND status = 'scheduled' AND next_run_at IS NOT NULL
		ORDER BY next_run_at LIMIT $2`, tenant, limit); err != nil {
		return out, err
	}
	err := scan(&out.Recent, `
		SELECT b.id, b.name, b.channel, b.status, b.repeat, r.started_at, r.scheduled_at
		FROM broadcasts b
		JOIN LATERAL (SELECT scheduled_at, started_at, finished_at FROM broadcast_runs
		              WHERE broadcast_id = b.id AND finished_at >= $2
		              ORDER BY finished_at DESC LIMIT 1) r ON true
		WHERE b.tenant_id = $1 AND b.status NOT IN ('running', 'testing')
		ORDER BY r.finished_at DESC LIMIT $3`, tenant, since, limit)
	return out, err
}
