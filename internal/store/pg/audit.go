package pg

// The audit log.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

func (s *Store) InsertAudit(ctx context.Context, tenant int64, e model.AuditEntry) error {
	var detail any
	if len(e.Detail) > 0 {
		detail = []byte(e.Detail)
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO audit_log (tenant_id, user_id, actor, action, object_type, object_id, status, ip, detail)
		VALUES ($1, nullif($2, 0), $3, $4, $5, $6, $7, $8, $9)`,
		tenant, e.UserID, e.Actor, e.Action, e.ObjectType, e.ObjectID, e.Status, e.IP, detail)
	return err
}

// ListAudit returns the newest entries first, filtered and paged by id.
func (s *Store) ListAudit(ctx context.Context, tenant int64, f model.AuditFilter) ([]model.AuditEntry, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	var from, to *time.Time
	if !f.From.IsZero() {
		from = &f.From
	}
	if !f.To.IsZero() {
		to = &f.To
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, at, coalesce(user_id, 0), actor, action, object_type, object_id, status, ip, detail FROM audit_log
		WHERE tenant_id = $1 AND ($2 = 0 OR id < $2) AND ($3 = 0 OR user_id = $3) AND ($4 = '' OR object_type = $4)
		  AND ($6::timestamptz IS NULL OR at >= $6) AND ($7::timestamptz IS NULL OR at < $7)
		ORDER BY id DESC LIMIT $5`, tenant, f.Before, f.UserID, f.ObjectType, f.Limit, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.AuditEntry, error) {
		var e model.AuditEntry
		var detail []byte
		err := r.Scan(&e.ID, &e.At, &e.UserID, &e.Actor, &e.Action, &e.ObjectType, &e.ObjectID, &e.Status, &e.IP, &detail)
		e.Detail = detail
		return e, err
	})
}
