package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"pulse/internal/kafka"
)

// errCancelled stops a run whose import was cancelled.
var errCancelled = errors.New("cancelled")

// JobStore is the background-import queue.
type JobStore interface {
	ClaimImport(ctx context.Context) (id, tenant, list int64, ok bool, err error)
	ImportReader(ctx context.Context, id int64) io.Reader
	ImportProgress(ctx context.Context, id, done, failed int64, errs any) (cancelled bool, err error)
	FinishImport(ctx context.Context, id int64, status, message string) error
	AttrTypes(ctx context.Context, tenant int64) (map[string]string, error)
}

// Jobs runs queued background imports, one at a time per worker.
type Jobs struct {
	Store JobStore
	Pub   kafka.Publisher
	Log   *slog.Logger
}

// RunDue runs every queued import.
func (j *Jobs) RunDue(ctx context.Context) error {
	for ctx.Err() == nil {
		id, tenant, list, ok, err := j.Store.ClaimImport(ctx)
		if err != nil || !ok {
			return err
		}
		j.run(ctx, id, tenant, list)
	}
	return ctx.Err()
}

func (j *Jobs) run(ctx context.Context, id, tenant, list int64) {
	start := time.Now()
	types, err := j.Store.AttrTypes(ctx, tenant)
	if err != nil {
		j.Log.Error("import", "import", id, "err", err)
		return // the claim expires and it's retried
	}
	last := time.Time{}
	res, err := Run(ctx, j.Pub, j.Store.ImportReader(ctx, id), Options{Tenant: tenant, ImportID: "job" + strconv.FormatInt(id, 10),
		ListID: list, Types: types, Now: time.Now(),
		Progress: func(r Result) error {
			if time.Since(last) < time.Second {
				return nil
			}
			last = time.Now()
			cancelled, err := j.Store.ImportProgress(ctx, id, r.Imported, r.Skipped(), r.Errors)
			if err == nil && cancelled {
				return errCancelled
			}
			return err
		}})
	switch {
	case errors.Is(err, errCancelled):
		j.Log.Info("import cancelled", "import", id, "rows", res.Rows)
		return
	case errors.Is(err, ErrBadHeader):
		_ = j.finish(ctx, id, res, "failed", strings.TrimPrefix(err.Error(), ErrBadHeader.Error()+": "))
		return
	case errors.Is(err, ErrMalformed):
		_ = j.finish(ctx, id, res, "failed", err.Error())
		return
	case errors.Is(err, ErrLineTooLong):
		_ = j.finish(ctx, id, res, "failed", fmt.Sprintf("Stopped at row %d: %v", res.Rows+1, ErrLineTooLong))
		return
	case err != nil:
		// Event bus or database trouble: leave it running; the claim expires and the import starts over.
		j.Log.Error("import stopped", "import", id, "rows", res.Rows, "err", err)
		return
	}
	msg := fmt.Sprintf("Imported %d of %d rows in %s.", res.Imported, res.Rows, time.Since(start).Round(time.Second))
	if err := j.finish(ctx, id, res, "done", msg); err != nil {
		j.Log.Error("finish import", "import", id, "err", err)
	}
	j.Log.Info("import done", "import", id, "rows", res.Rows, "imported", res.Imported, "took", time.Since(start))
}

func (j *Jobs) finish(ctx context.Context, id int64, res Result, status, msg string) error {
	if _, err := j.Store.ImportProgress(ctx, id, res.Imported, res.Skipped(), res.Errors); err != nil {
		return err
	}
	return j.Store.FinishImport(ctx, id, status, msg)
}
