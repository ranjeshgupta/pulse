package pg

// Campaign approvals (O6).

import (
	"context"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

const approvalCols = `a.id, a.tenant_id, a.kind, a.campaign_id, a.version, a.requested_by, a.requester, a.note, a.status,
	a.decided_by, a.decider, a.decision_note, a.created_at, a.decided_at,
	coalesce(CASE a.kind WHEN 'broadcast' THEN (SELECT name FROM broadcasts WHERE id = a.campaign_id AND tenant_id = a.tenant_id)
		ELSE (SELECT name FROM journeys WHERE id = a.campaign_id AND tenant_id = a.tenant_id) END, '')`

func scanApproval(r pgx.Row) (model.Approval, error) {
	var a model.Approval
	err := r.Scan(&a.ID, &a.TenantID, &a.Kind, &a.CampaignID, &a.Version, &a.RequestedBy, &a.Requester, &a.Note, &a.Status,
		&a.DecidedBy, &a.Decider, &a.DecisionNote, &a.CreatedAt, &a.DecidedAt, &a.CampaignName)
	return a, notFound(err)
}

// RequestApproval records a request, withdrawing any earlier pending one for the campaign.
func (s *Store) RequestApproval(ctx context.Context, a model.Approval) (int64, error) {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE approval_requests SET status = 'withdrawn', decided_at = now()
			WHERE tenant_id = $1 AND kind = $2 AND campaign_id = $3 AND status = 'pending'`, a.TenantID, a.Kind, a.CampaignID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO approval_requests (tenant_id, kind, campaign_id, version, requested_by, requester, note)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, a.TenantID, a.Kind, a.CampaignID, a.Version, a.RequestedBy, a.Requester, a.Note).Scan(&a.ID)
	})
	return a.ID, err
}

// ListApprovals lists a workspace's requests (status "" for every status), newest first.
func (s *Store) ListApprovals(ctx context.Context, tenant int64, status string, limit int) ([]model.Approval, error) {
	rows, err := s.db.Query(ctx, `SELECT `+approvalCols+` FROM approval_requests a
		WHERE a.tenant_id = $1 AND ($2 = '' OR a.status = $2) ORDER BY a.created_at DESC LIMIT $3`, tenant, status, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Approval, error) { return scanApproval(r) })
}

// GetApproval loads one request.
func (s *Store) GetApproval(ctx context.Context, tenant, id int64) (model.Approval, error) {
	return scanApproval(s.db.QueryRow(ctx, `SELECT `+approvalCols+` FROM approval_requests a WHERE a.tenant_id = $1 AND a.id = $2`, tenant, id))
}

// LatestApproval is a campaign's latest request (ErrNotFound without one).
func (s *Store) LatestApproval(ctx context.Context, tenant int64, kind string, id int64) (model.Approval, error) {
	return scanApproval(s.db.QueryRow(ctx, `SELECT `+approvalCols+` FROM approval_requests a
		WHERE a.tenant_id = $1 AND a.kind = $2 AND a.campaign_id = $3 ORDER BY a.created_at DESC LIMIT 1`, tenant, kind, id))
}

// DecideApproval settles a pending request; false when it was no longer pending.
func (s *Store) DecideApproval(ctx context.Context, tenant, id int64, status string, by int64, decider, note string) (bool, error) {
	tag, err := s.db.Exec(ctx, `UPDATE approval_requests SET status = $3, decided_by = $4, decider = $5, decision_note = $6, decided_at = now()
		WHERE tenant_id = $1 AND id = $2 AND status = 'pending'`, tenant, id, status, by, decider, note)
	return tag.RowsAffected() == 1, err
}
