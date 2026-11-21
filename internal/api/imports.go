package api

// Background CSV imports, for files of any size: the upload is stored, a worker streams it through
// the pipeline, and the app shows progress.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"pulse/internal/model"
)

const (
	importChunk       = 4 << 20
	maxBackgroundSize = 10 << 30 // 10 GB

	// Per-workspace quota: background imports store the whole file in Postgres first, so one
	// workspace can't fill the database or monopolise the import workers.
	maxConcurrentImports = 2
	maxImportBytesPerDay = 20 << 30 // 20 GB a day (two of the largest files)
)

// importQuota checks the workspace's imports before a new one: at most maxConcurrentImports
// uploading, queued or running, and maxImportBytesPerDay uploaded in the last 24 hours. It returns
// the bytes still allowed today, or a message saying why not.
func (s *Server) importQuota(ctx context.Context, tenant int64) (int64, string, error) {
	list, err := s.imports().ListImports(ctx, tenant)
	if err != nil {
		return 0, "", err
	}
	now := s.now()
	active, used := 0, int64(0)
	for _, im := range list {
		switch im.Status {
		case "queued", "running":
			active++
		case "uploading":
			if now.Sub(im.CreatedAt) < time.Hour { // an upload that died long ago doesn't count
				active++
			}
		}
		if now.Sub(im.CreatedAt) < 24*time.Hour {
			used += im.Bytes
		}
	}
	if active >= maxConcurrentImports {
		return 0, fmt.Sprintf("%d imports are already in progress in this workspace; wait for one to finish", active), nil
	}
	left := int64(maxImportBytesPerDay) - used
	if left <= 0 {
		return 0, "this workspace has imported 20 GB in the last 24 hours, the most allowed; try again later", nil
	}
	return left, "", nil
}

type ImportStore interface {
	CreateImport(ctx context.Context, tenant, user int64, filename string, listID int64) (int64, error)
	AppendImportChunk(ctx context.Context, id int64, seq int, data []byte) error
	QueueImport(ctx context.Context, id int64, failMessage string) error
	GetImport(ctx context.Context, tenant, id int64) (model.Import, error)
	ListImports(ctx context.Context, tenant int64) ([]model.Import, error)
	CancelImport(ctx context.Context, tenant, id int64) error
}

func (s *Server) backgroundImportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/imports", s.authed(s.startImport))
	mux.HandleFunc("GET /v1/imports", s.tenant(s.listImports))
	mux.HandleFunc("GET /v1/imports/{id}", s.tenant(s.getImport))
	mux.HandleFunc("POST /v1/imports/{id}/cancel", s.tenant(s.cancelImport))
}

func (s *Server) imports() ImportStore { return s.Store.(ImportStore) }

// startImport stores an upload (multipart field "file", or a text/csv body; ?list_id= and
// ?filename= are optional) and queues it. It answers once the upload is stored.
func (s *Server) startImport(w http.ResponseWriter, r *http.Request, p model.Principal) {
	listID, ok := s.importList(w, r, p.TenantID)
	if !ok {
		return
	}
	allowed, why, err := s.importQuota(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if why != "" {
		writeErr(w, http.StatusTooManyRequests, why)
		return
	}
	if r.ContentLength > allowed {
		writeErr(w, http.StatusTooManyRequests, "this file is larger than what this workspace may still import today")
		return
	}
	src, name := io.Reader(r.Body), r.URL.Query().Get("filename")
	if ct, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); strings.HasPrefix(ct, "multipart/") {
		mr, err := r.MultipartReader()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid multipart body")
			return
		}
		_ = params
		for {
			part, err := mr.NextPart()
			if err != nil {
				writeErr(w, http.StatusBadRequest, "attach the CSV as the \"file\" field")
				return
			}
			if part.FormName() == "file" {
				src, name = part, part.FileName()
				break
			}
		}
	}
	name = filepath.Base(name)
	if name == "." || name == "/" {
		name = ""
	}
	id, err := s.imports().CreateImport(r.Context(), p.TenantID, p.UserID, name, listID)
	if err != nil {
		s.fail(w, err)
		return
	}
	buf := make([]byte, importChunk)
	var total int64
	for seq := 0; ; seq++ {
		n, rerr := io.ReadFull(src, buf)
		if n > 0 {
			total += int64(n)
			if total > allowed {
				_ = s.imports().QueueImport(context.WithoutCancel(r.Context()), id, "the daily import limit was reached")
				writeErr(w, http.StatusTooManyRequests, "this file is larger than what this workspace may still import today")
				return
			}
			if err := s.imports().AppendImportChunk(r.Context(), id, seq, buf[:n]); err != nil {
				_ = s.imports().QueueImport(context.WithoutCancel(r.Context()), id, "the upload couldn't be stored")
				s.fail(w, err)
				return
			}
		}
		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			_ = s.imports().QueueImport(context.WithoutCancel(r.Context()), id, "the upload was interrupted")
			writeErr(w, http.StatusBadRequest, "the upload was interrupted or is too large (10 GB at most)")
			return
		}
	}
	if r.ContentLength > 0 && total != r.ContentLength { // the connection dropped part-way
		_ = s.imports().QueueImport(context.WithoutCancel(r.Context()), id, "the upload was interrupted")
		writeErr(w, http.StatusBadRequest, "the upload was interrupted; try again")
		return
	}
	if total == 0 {
		_ = s.imports().QueueImport(r.Context(), id, "the file is empty")
		writeErr(w, http.StatusBadRequest, "the file is empty")
		return
	}
	if err := s.imports().QueueImport(r.Context(), id, ""); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "status": "queued", "bytes": total})
}

func (s *Server) listImports(w http.ResponseWriter, r *http.Request, tenant int64) {
	ims, err := s.imports().ListImports(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if ims == nil {
		ims = []model.Import{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"imports": ims})
}

func (s *Server) getImport(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	im, err := s.imports().GetImport(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, im)
}

func (s *Server) cancelImport(w http.ResponseWriter, r *http.Request, tenant int64) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.imports().CancelImport(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "cancelled"})
}
