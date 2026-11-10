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

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/auth"
	"pulse/internal/event"
	"pulse/internal/kafka"
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

type rowError struct {
	Row   int    `json:"row"`
	Error string `json:"error"`
}

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
	cr := csv.NewReader(src)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the file is empty or not a CSV")
		return
	}
	cols := make([]string, len(header))
	idCol, emailCol := -1, -1
	var bad []string
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		h = strings.ReplaceAll(h, " ", "_")
		cols[i] = h
		switch {
		case h == "external_id" || h == "id" || h == "user_id":
			idCol = i
		case h == "email":
			emailCol = i
		case !attr.ValidKey(h):
			bad = append(bad, header[i])
		}
	}
	if len(bad) > 0 {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("rename these columns to letters, digits and _ (starting with a letter): %s", strings.Join(bad, ", ")))
		return
	}
	if idCol < 0 && emailCol < 0 {
		writeErr(w, http.StatusBadRequest, "the file needs an external_id or email column")
		return
	}

	// Optional ?list_id= adds every imported contact to that list.
	var listID int64
	if v := r.URL.Query().Get("list_id"); v != "" {
		if listID, err = strconv.ParseInt(v, 10, 64); err != nil || !s.isList(w, r, tenant, listID) {
			if err != nil {
				writeErr(w, http.StatusBadRequest, "invalid list_id")
			}
			return
		}
	}
	types := map[string]string{}
	if a, ok := s.Store.(AudienceStore); ok {
		defs, err := a.AttributeDefs(r.Context(), tenant)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, d := range defs {
			types[d.Key] = d.Type
		}
	}

	importID := auth.NewToken("")[:10]
	now := s.now()
	var recs []*kgo.Record
	var errs []rowError
	rows, imported := 0, 0
	flush := func() error {
		if len(recs) == 0 {
			return nil
		}
		err := s.Pub.Publish(r.Context(), recs...)
		recs = recs[:0]
		return err
	}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		rows++
		line := rows + 1 // 1-based, after the header
		if rows > maxImportRows {
			writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("files are limited to %d rows; split it", maxImportRows))
			return
		}
		if err != nil {
			errs = appendErr(errs, line, "can't be read as CSV: "+err.Error())
			continue
		}
		get := func(i int) string {
			if i < 0 || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		ext, email := get(idCol), get(emailCol)
		if ext == "" {
			ext = strings.ToLower(email)
		}
		if ext == "" {
			errs = appendErr(errs, line, "no external_id or email")
			continue
		}
		attrs := map[string]any{}
		for i, c := range cols {
			if i == idCol || i == emailCol || c == "" {
				continue
			}
			v := get(i)
			if v == "" {
				continue // empty cells never erase existing values
			}
			if c == "phone" {
				v = normalizePhone(v)
			}
			attrs[c] = v
		}
		if err := coerceAttrs(attrs, types); err != nil {
			errs = appendErr(errs, line, err.Error())
			continue
		}
		evs := []event.Event{{ID: fmt.Sprintf("imp:%s:%s", importID, ext), TenantID: tenant, ExternalID: ext, Name: event.Identify, Email: email, Attrs: attrs}}
		if listID != 0 {
			evs = append(evs, event.Event{ID: fmt.Sprintf("imp:%s:%s:list", importID, ext), TenantID: tenant, ExternalID: ext,
				Name: event.ListJoined, Props: map[string]any{"list_id": listID, "source": "import"}})
		}
		var rowErr error
		var krs []*kgo.Record
		for i := range evs {
			if rowErr = evs[i].Normalize(now); rowErr != nil {
				break
			}
			kr, err := kafka.JSON(event.TopicRaw, evs[i].RawKey(), evs[i])
			if err != nil {
				rowErr = err
				break
			}
			krs = append(krs, kr)
		}
		if rowErr != nil {
			errs = appendErr(errs, line, rowErr.Error())
			continue
		}
		recs = append(recs, krs...)
		imported++
		if len(recs) >= importBatch {
			if err := flush(); err != nil {
				writeErr(w, http.StatusServiceUnavailable, fmt.Sprintf("stopped at row %d: event bus unavailable; %d rows were imported, upload the file again to finish", line, imported-importBatch))
				return
			}
		}
	}
	if err := flush(); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "event bus unavailable; upload the file again")
		return
	}
	if errs == nil {
		errs = []rowError{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "imported": imported, "skipped": rows - imported, "errors": errs, "columns": cols})
}

func appendErr(errs []rowError, row int, msg string) []rowError {
	if len(errs) < maxRowErrors {
		errs = append(errs, rowError{Row: row, Error: msg})
	}
	return errs
}

// normalizePhone strips spaces, dashes, dots and brackets. Numbers without a country code are kept
// as they are: guessing a country would silently send to the wrong place.
func normalizePhone(p string) string {
	return strings.NewReplacer(" ", "", "-", "", ".", "", "(", "", ")", "").Replace(p)
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
			row := []string{c.ExternalID, c.Email, c.CreatedAt.UTC().Format(time.RFC3339), c.UpdatedAt.UTC().Format(time.RFC3339)}
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
