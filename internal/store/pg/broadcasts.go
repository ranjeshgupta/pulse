package pg

// Broadcasts: a template sent to a segment, once or on a repeat.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"pulse/internal/broadcast"
	"pulse/internal/model"
)

const broadcastCols = `id, tenant_id, name, channel, template_id, coalesce(fallback_template_id, 0), segment_id,
	start_at, end_at, repeat, next_run_at, status, updated_at, ab, conversion_event, conversion_days,
	exclude_segment_ids, local_time, timezone, include_segment_ids, deadline, tags, coalesce(provider_id, 0), topic, send_per_hour, holdout_percent`

func scanBroadcast(row pgx.Row) (model.Broadcast, error) {
	var b model.Broadcast
	var ab, deadline []byte
	err := row.Scan(&b.ID, &b.TenantID, &b.Name, &b.Channel, &b.TemplateID, &b.FallbackTemplateID, &b.SegmentID,
		&b.StartAt, &b.EndAt, &b.Repeat, &b.NextRunAt, &b.Status, &b.UpdatedAt, &ab, &b.ConversionEvent, &b.ConversionDays,
		&b.ExcludeSegmentIDs, &b.LocalTime, &b.Timezone, &b.IncludeSegmentIDs, &deadline, &b.Tags, &b.ProviderID, &b.Topic, &b.SendPerHour, &b.HoldoutPercent)
	if err == nil && len(ab) > 0 && string(ab) != "null" {
		b.AB = &model.ABTest{}
		err = json.Unmarshal(ab, b.AB)
	}
	if err == nil && len(deadline) > 0 && string(deadline) != "null" {
		b.Deadline = &model.Deadline{}
		err = json.Unmarshal(deadline, b.Deadline)
	}
	if b.Tags == nil {
		b.Tags = []string{}
	}
	return b, notFound(err)
}

func deadlineJSON(d *model.Deadline) []byte {
	if d == nil {
		return nil
	}
	b, _ := json.Marshal(d)
	return b
}

// tags is a never-nil list for text[] NOT NULL columns.
func tags(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func abJSON(ab *model.ABTest) []byte {
	if ab == nil {
		return nil
	}
	b, _ := json.Marshal(ab)
	return b
}

// ids is a never-nil list for bigint[] NOT NULL columns.
func ids(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// SaveBroadcast creates (ID 0) or updates a broadcast that is not sending or waiting for an A/B
// winner. Saving puts it back in draft.
func (s *Store) SaveBroadcast(ctx context.Context, b model.Broadcast) (int64, error) {
	if b.ID == 0 {
		err := s.db.QueryRow(ctx, `
			INSERT INTO broadcasts (tenant_id, name, channel, template_id, fallback_template_id, segment_id, start_at, end_at, repeat,
				ab, conversion_event, conversion_days, exclude_segment_ids, local_time, timezone, include_segment_ids, deadline, tags, provider_id, topic, send_per_hour, holdout_percent)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22) RETURNING id`,
			b.TenantID, b.Name, b.Channel, b.TemplateID, nullID(b.FallbackTemplateID), b.SegmentID, b.StartAt, b.EndAt, b.Repeat,
			abJSON(b.AB), b.ConversionEvent, b.ConversionDays, ids(b.ExcludeSegmentIDs), b.LocalTime, b.Timezone,
			ids(b.IncludeSegmentIDs), deadlineJSON(b.Deadline), tags(b.Tags), nullID(b.ProviderID), b.Topic, b.SendPerHour, b.HoldoutPercent).Scan(&b.ID)
		return b.ID, err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE broadcasts SET name = $3, channel = $4, template_id = $5, fallback_template_id = $6, segment_id = $7,
			start_at = $8, end_at = $9, repeat = $10, ab = $11, conversion_event = $12, conversion_days = $13,
			exclude_segment_ids = $14, local_time = $15, timezone = $16,
			include_segment_ids = $17, deadline = $18, tags = $19, provider_id = $20, topic = $21, send_per_hour = $22, holdout_percent = $23,
			status = 'draft', next_run_at = NULL, updated_at = now()
		WHERE tenant_id = $1 AND id = $2 AND status NOT IN ('running', 'testing')`,
		b.TenantID, b.ID, b.Name, b.Channel, b.TemplateID, nullID(b.FallbackTemplateID), b.SegmentID, b.StartAt, b.EndAt, b.Repeat,
		abJSON(b.AB), b.ConversionEvent, b.ConversionDays, ids(b.ExcludeSegmentIDs), b.LocalTime, b.Timezone,
		ids(b.IncludeSegmentIDs), deadlineJSON(b.Deadline), tags(b.Tags), nullID(b.ProviderID), b.Topic, b.SendPerHour, b.HoldoutPercent)
	if err == nil && tag.RowsAffected() == 0 {
		cur, err := s.GetBroadcast(ctx, b.TenantID, b.ID)
		if err != nil {
			return 0, err
		}
		if cur.Status == "testing" {
			return 0, errorString("this A/B test is waiting for its winner; pick it or cancel the broadcast first")
		}
		return 0, errorString("this broadcast is sending right now; try again in a moment")
	}
	return b.ID, err
}

// SetBroadcastTags changes a broadcast's tags in any status (they don't affect sending).
func (s *Store) SetBroadcastTags(ctx context.Context, tenant, id int64, t []string) error {
	tag, err := s.db.Exec(ctx, `UPDATE broadcasts SET tags = $3 WHERE tenant_id = $1 AND id = $2`, tenant, id, tags(t))
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

func (s *Store) GetBroadcast(ctx context.Context, tenant, id int64) (model.Broadcast, error) {
	return scanBroadcast(s.db.QueryRow(ctx, `SELECT `+broadcastCols+` FROM broadcasts WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

func (s *Store) ListBroadcasts(ctx context.Context, tenant int64) ([]model.Broadcast, error) {
	rows, err := s.db.Query(ctx, `SELECT `+broadcastCols+` FROM broadcasts WHERE tenant_id = $1 ORDER BY id DESC`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Broadcast, error) { return scanBroadcast(r) })
}

func (s *Store) DeleteBroadcast(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM broadcasts WHERE tenant_id = $1 AND id = $2 AND status NOT IN ('running', 'testing')`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// SetBroadcastSchedule moves a broadcast to scheduled (next run at next) or, with next nil, to a final status.
func (s *Store) SetBroadcastSchedule(ctx context.Context, tenant, id int64, status string, next *time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE broadcasts SET status = $3, next_run_at = $4, updated_at = now()
		WHERE tenant_id = $1 AND id = $2 AND status <> 'running'`, tenant, id, status, next)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// ClaimDueBroadcast takes one broadcast whose run is due (SKIP LOCKED, so workers never share one).
// A worker that crashed mid-run loses its claim after 10 minutes; message ids make the re-run safe.
func (s *Store) ClaimDueBroadcast(ctx context.Context) (model.Broadcast, bool, error) {
	b, err := scanBroadcast(s.db.QueryRow(ctx, `
		UPDATE broadcasts SET status = 'running', claimed_at = now()
		WHERE id = (
			SELECT id FROM broadcasts
			WHERE (status = 'scheduled' AND next_run_at <= now())
			   OR (status = 'running' AND claimed_at < now() - interval '10 minutes')
			ORDER BY next_run_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING `+broadcastCols))
	if err == ErrNotFound {
		return b, false, nil
	}
	return b, err == nil, err
}

// StartRun records a run for one scheduled time (idempotent across retries of the same run).
func (s *Store) StartRun(ctx context.Context, broadcast int64, scheduledAt time.Time) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `
		INSERT INTO broadcast_runs (broadcast_id, scheduled_at) VALUES ($1, $2)
		ON CONFLICT (broadcast_id, scheduled_at) DO UPDATE SET started_at = now()
		RETURNING id`, broadcast, scheduledAt).Scan(&id)
	return id, err
}

// SetRunAudience stores a run's audience when it started (the report's funnel).
func (s *Store) SetRunAudience(ctx context.Context, run int64, audience []byte) error {
	_, err := s.db.Exec(ctx, `UPDATE broadcast_runs SET audience = $2 WHERE id = $1`, run, audience)
	return err
}

func (s *Store) FinishRun(ctx context.Context, run int64, recipients, testRecipients int, winnerDue *time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE broadcast_runs SET finished_at = now(), recipients = $2, test_recipients = $3, winner_due_at = $4
		WHERE id = $1`, run, recipients, testRecipients, winnerDue)
	return err
}

// AdvanceBroadcast releases the claim: scheduled for the next run; with none, testing while an A/B
// winner is still to be sent, else completed.
func (s *Store) AdvanceBroadcast(ctx context.Context, id int64, next *time.Time, testing bool) error {
	status := "completed"
	switch {
	case next != nil:
		status = "scheduled"
	case testing:
		status = "testing"
	}
	_, err := s.db.Exec(ctx, `UPDATE broadcasts SET status = $2, next_run_at = $3, claimed_at = NULL, updated_at = now() WHERE id = $1`,
		id, status, next)
	return err
}

// ClaimDueWinner takes one A/B run whose winner is due (its wait is over, or someone picked it).
// A worker that crashed loses the claim after 10 minutes; message ids make the retry safe.
func (s *Store) ClaimDueWinner(ctx context.Context) (broadcast.WinnerRun, bool, error) {
	var w broadcast.WinnerRun
	var bid int64
	var winner *string
	err := s.db.QueryRow(ctx, `
		UPDATE broadcast_runs SET winner_claimed_at = now()
		WHERE id = (
			SELECT id FROM broadcast_runs
			WHERE winner_sent_at IS NULL AND winner_due_at <= now()
			  AND (winner_claimed_at IS NULL OR winner_claimed_at < now() - interval '10 minutes')
			ORDER BY winner_due_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING id, broadcast_id, scheduled_at, winner`).Scan(&w.RunID, &bid, &w.ScheduledAt, &winner)
	if err == pgx.ErrNoRows {
		return w, false, nil
	} else if err != nil {
		return w, false, err
	}
	if winner != nil {
		w.Winner = *winner
	}
	w.Broadcast, err = scanBroadcast(s.db.QueryRow(ctx, `SELECT `+broadcastCols+` FROM broadcasts WHERE id = $1`, bid))
	return w, err == nil, err
}

// FinishWinner records the winner's send and completes a one-time broadcast that was testing.
func (s *Store) FinishWinner(ctx context.Context, run, broadcastID int64, winner, reason string, recipients int) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE broadcast_runs SET winner = nullif($2, ''), winner_reason = $3, winner_recipients = $4, winner_sent_at = now()
			WHERE id = $1`, run, winner, reason, recipients); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE broadcasts SET status = 'completed', updated_at = now() WHERE id = $1 AND status = 'testing'`, broadcastID)
		return err
	})
}

// PickWinner chooses a run's winning variant by hand; the runner sends it within seconds.
func (s *Store) PickWinner(ctx context.Context, tenant, broadcastID, run int64, variant string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE broadcast_runs r SET winner = $4, winner_due_at = now()
		FROM broadcasts b
		WHERE b.id = r.broadcast_id AND b.tenant_id = $1 AND b.id = $2 AND r.id = $3
		  AND r.winner_sent_at IS NULL AND r.finished_at IS NOT NULL`, tenant, broadcastID, run, variant)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

func (s *Store) BroadcastRuns(ctx context.Context, tenant, id int64) ([]model.BroadcastRun, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.scheduled_at, r.started_at, r.finished_at, r.recipients,
			r.test_recipients, r.winner_due_at, coalesce(r.winner, ''), r.winner_reason, r.winner_sent_at, r.winner_recipients, r.audience
		FROM broadcast_runs r
		JOIN broadcasts b ON b.id = r.broadcast_id WHERE b.tenant_id = $1 AND b.id = $2 ORDER BY r.scheduled_at DESC LIMIT 50`,
		tenant, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[model.BroadcastRun])
}
