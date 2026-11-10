package api

// CSV contact import and export.

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"pulse/internal/attr"
	"pulse/internal/auth"
	"pulse/internal/importer"
	"pulse/internal/model"
)

const (
	maxImportBytes = 200 << 20
	maxImportRows  = 2_000_000
	importBatch    = 1000
	maxRowErrors   = 50
)

type ExportStore interface {
	SearchContacts(ctx context.Context, tenant int64, q string, afterID uint32, limit int) ([]model.Contact, error)
	AttrKeys(ctx context.Context, tenant int64) ([]string, error)
}

func (s *Server) importRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/contacts/import", s.tenant(s.importContacts))
	mux.HandleFunc("GET /v1/contacts/export", s.tenant(s.exportContacts))
}

type rowError = importer.RowError

// importContacts reads a CSV (multipart field "file", or a text/csv body). The header row names the
// columns: external_id (or email, used as the ID when there's no external_id), email, phone, and any
// attribute. Rows become identify events, so imported contacts flow into segments and journeys like
// live data. Event ids are derived from the file's content, so uploading the same file twice is harmless.
func (s *Server) importContacts(w http.ResponseWriter, r *http.Request, tenant int64) {
	var src io.Reader = r.Body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		f, _, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "attach the CSV as the \"file\" field")
			return
		}
		defer f.Close()
		src = f
	}
	// Optional ?list_id= adds every imported contact to that list.
	listID, ok := s.importList(w, r, tenant)
	if !ok {
		return
	}
	types, err := s.attrTypes(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	res, err := importer.Run(r.Context(), s.Pub, src, importer.Options{Tenant: tenant, ImportID: auth.NewToken("")[:10],
		ListID: listID, Types: types, MaxRows: maxImportRows, Now: s.now()})
	switch {
	case errors.Is(err, importer.ErrBadHeader):
		writeErr(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), importer.ErrBadHeader.Error()+": "))
		return
	case errors.Is(err, importer.ErrTooManyRows):
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("files here are limited to %d rows; use a background import (POST /v1/imports) for more", maxImportRows))
		return
	case err != nil:
		writeErr(w, http.StatusServiceUnavailable, fmt.Sprintf("stopped after %d rows: event bus unavailable; upload the file again to finish", res.Rows))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": res.Rows, "imported": res.Imported, "skipped": res.Skipped(), "errors": res.Errors, "columns": res.Columns})
}

// importList reads ?list_id=, which must be a static list (0 when absent).
func (s *Server) importList(w http.ResponseWriter, r *http.Request, tenant int64) (int64, bool) {
	v := r.URL.Query().Get("list_id")
	if v == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid list_id")
		return 0, false
	}
	return id, s.isList(w, r, tenant, id)
}

// attrTypes returns the workspace's attribute types, for converting imported text.
func (s *Server) attrTypes(ctx context.Context, tenant int64) (map[string]string, error) {
	types := map[string]string{}
	if a, ok := s.Store.(AudienceStore); ok {
		defs, err := a.AttributeDefs(ctx, tenant)
		if err != nil {
			return nil, err
		}
		for _, d := range defs {
			types[d.Key] = d.Type
		}
	}
	return types, nil
}

// exportContacts streams every contact as CSV: fixed columns, then one column per attribute.
func (s *Server) exportContacts(w http.ResponseWriter, r *http.Request, tenant int64) {
	st := s.Store.(ExportStore)
	keys, err := st.AttrKeys(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	sort.Strings(keys)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="contacts-%s.csv"`, s.now().Format("2006-01-02")))
	cw := csv.NewWriter(w)
	_ = cw.Write(append([]string{"external_id", "email", "created_at", "updated_at"}, keys...))
	var after uint32
	for {
		page, err := st.SearchContacts(r.Context(), tenant, "", after, 1000)
		if err != nil {
			s.Log.Error("export", "err", err) // headers are sent: the file ends early
			return
		}
		for _, c := range page {
			row := []string{csvSafe(c.ExternalID), csvSafe(c.Email), c.CreatedAt.UTC().Format(time.RFC3339), c.UpdatedAt.UTC().Format(time.RFC3339)}
			for _, k := range keys {
				v, ok := c.Attrs[k]
				if !ok {
					row = append(row, "")
					continue
				}
				if m, isMap := v.(map[string]any); isMap {
					b, _ := json.Marshal(m)
					row = append(row, string(b))
					continue
				}
				row = append(row, csvSafe(attr.String(v)))
			}
			_ = cw.Write(row)
		}
		cw.Flush()
		if len(page) < 1000 {
			return
		}
		after = page[len(page)-1].ID
	}
}

// csvSafe defuses spreadsheet formula injection: a cell starting with = + - @ is prefixed with '.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		if _, err := strconv.ParseFloat(v, 64); err != nil { // keep negative numbers as numbers
			return "'" + v
		}
	}
	return v
}
