package pg

// Background imports: the upload is kept in chunks until a worker has streamed it through.

import (
	"context"
	"encoding/json"
	"io"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

const importCols = `id, filename, coalesce(list_id, 0), status, bytes, rows_done, rows_failed, errors, message, created_at, started_at, finished_at`

func scanImport(row pgx.Row) (model.Import, error) {
	var im model.Import
	var errs []byte
	err := row.Scan(&im.ID, &im.Filename, &im.ListID, &im.Status, &im.Bytes, &im.RowsDone, &im.RowsFailed, &errs, &im.Message,
		&im.CreatedAt, &im.StartedAt, &im.FinishedAt)
	im.Errors = errs
	return im, notFound(err)
}

func (s *Store) CreateImport(ctx context.Context, tenant, user int64, filename string, listID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO imports (tenant_id, user_id, filename, list_id, status) VALUES ($1, nullif($2, 0), $3, nullif($4, 0), 'uploading') RETURNING id`,
		tenant, user, filename, listID).Scan(&id)
	return id, err
}

func (s *Store) AppendImportChunk(ctx context.Context, id int64, seq int, data []byte) error {
	_, err := s.db.Exec(ctx, `INSERT INTO import_chunks (import_id, seq, data) VALUES ($1, $2, $3)`, id, seq, data)
	if err == nil {
		_, err = s.db.Exec(ctx, `UPDATE imports SET bytes = bytes + $2 WHERE id = $1`, id, len(data))
	}
	return err
}

// QueueImport hands a finished upload to the workers; a failed upload is marked failed.
func (s *Store) QueueImport(ctx context.Context, id int64, failMessage string) error {
	if failMessage != "" {
		_, err := s.db.Exec(ctx, `UPDATE imports SET status = 'failed', message = $2, finished_at = now() WHERE id = $1`, id, failMessage)
		if err == nil {
			_, err = s.db.Exec(ctx, `DELETE FROM import_chunks WHERE import_id = $1`, id)
		}
		return err
	}
	_, err := s.db.Exec(ctx, `UPDATE imports SET status = 'queued' WHERE id = $1 AND status = 'uploading'`, id)
	return err
}

func (s *Store) GetImport(ctx context.Context, tenant, id int64) (model.Import, error) {
	return scanImport(s.db.QueryRow(ctx, `SELECT `+importCols+` FROM imports WHERE tenant_id = $1 AND id = $2`, tenant, id))
}

func (s *Store) ListImports(ctx context.Context, tenant int64) ([]model.Import, error) {
	rows, err := s.db.Query(ctx, `SELECT `+importCols+` FROM imports WHERE tenant_id = $1 ORDER BY id DESC LIMIT 50`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Import, error) { return scanImport(r) })
}

// CancelImport stops a queued or running import; the worker notices at its next progress update.
func (s *Store) CancelImport(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `UPDATE imports SET status = 'cancelled', finished_at = now()
		WHERE tenant_id = $1 AND id = $2 AND status IN ('uploading', 'queued', 'running')`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	if err == nil {
		_, err = s.db.Exec(ctx, `DELETE FROM import_chunks WHERE import_id = $1`, id)
	}
	return err
}

// ClaimImport takes the oldest queued import, or one whose worker stopped reporting for 10 minutes
// (it starts over; event ids make the re-run harmless).
func (s *Store) ClaimImport(ctx context.Context) (id, tenant, list int64, ok bool, err error) {
	err = s.db.QueryRow(ctx, `
		UPDATE imports SET status = 'running', claimed_at = now(), started_at = coalesce(started_at, now()), rows_done = 0, rows_failed = 0
		WHERE id = (SELECT id FROM imports
			WHERE status = 'queued' OR (status = 'running' AND claimed_at < now() - interval '10 minutes')
			ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING id, tenant_id, coalesce(list_id, 0)`).Scan(&id, &tenant, &list)
	if err == pgx.ErrNoRows {
		return 0, 0, 0, false, nil
	}
	return id, tenant, list, err == nil, err
}

// AttrTypes maps attribute keys to their defined types.
func (s *Store) AttrTypes(ctx context.Context, tenant int64) (map[string]string, error) {
	defs, err := s.AttributeDefs(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, d := range defs {
		out[d.Key] = d.Type
	}
	return out, nil
}

// ImportProgress records progress and keeps the claim; it reports whether the import was cancelled.
func (s *Store) ImportProgress(ctx context.Context, id, done, failed int64, errs any) (bool, error) {
	b, _ := json.Marshal(errs)
	var status string
	err := s.db.QueryRow(ctx, `UPDATE imports SET rows_done = $2, rows_failed = $3, errors = $4, claimed_at = now()
		WHERE id = $1 RETURNING status`, id, done, failed, b).Scan(&status)
	return status == "cancelled", err
}

// FinishImport ends an import (done or failed) and drops its upload.
func (s *Store) FinishImport(ctx context.Context, id int64, status, message string) error {
	_, err := s.db.Exec(ctx, `UPDATE imports SET status = $2, message = $3, finished_at = now() WHERE id = $1 AND status = 'running'`, id, status, message)
	if err == nil {
		_, err = s.db.Exec(ctx, `DELETE FROM import_chunks WHERE import_id = $1`, id)
	}
	return err
}

// ImportReader streams an upload's chunks in order.
func (s *Store) ImportReader(ctx context.Context, id int64) io.Reader {
	return &chunkReader{ctx: ctx, s: s, id: id}
}

type chunkReader struct {
	ctx context.Context
	s   *Store
	id  int64
	seq int
	buf []byte
	eof bool
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.eof {
			return 0, io.EOF
		}
		err := r.s.db.QueryRow(r.ctx, `SELECT data FROM import_chunks WHERE import_id = $1 AND seq = $2`, r.id, r.seq).Scan(&r.buf)
		if err == pgx.ErrNoRows {
			r.eof = true
			continue
		} else if err != nil {
			return 0, err
		}
		r.seq++
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}
