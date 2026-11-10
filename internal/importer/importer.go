// Package importer turns a contacts CSV into identify events (and list_joined, with a list).
//
// The header row names the columns: external_id (or id / user_id), email (the id when there's no
// external_id), phone, and any attribute. Rows become events on the live pipeline, so imported
// contacts move segments and journeys like any other data. Event ids come from the import id and
// the contact, so re-running the same import never counts twice.
package importer

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"pulse/internal/attr"
	"pulse/internal/event"
	"pulse/internal/kafka"
)

const (
	batch     = 1000
	MaxErrors = 50
)

// ErrBadHeader wraps problems with the header row (the whole file is refused).
var ErrBadHeader = errors.New("bad header")

type RowError struct {
	Row   int64  `json:"row"`
	Error string `json:"error"`
}

type Options struct {
	Tenant   int64
	ImportID string            // makes event ids; the same id re-imports harmlessly
	ListID   int64             // add every contact to this list (0: none)
	Types    map[string]string // attribute definitions: number and boolean columns are converted
	MaxRows  int64             // 0: no limit
	Now      time.Time
	// Progress is called after every published batch; returning an error stops the import.
	Progress func(r Result) error
}

type Result struct {
	Rows     int64      `json:"rows"`
	Imported int64      `json:"imported"`
	Errors   []RowError `json:"errors"`
	Columns  []string   `json:"columns"`
}

func (r Result) Skipped() int64 { return r.Rows - r.Imported }

// ErrTooManyRows stops an import over Options.MaxRows.
var ErrTooManyRows = errors.New("too many rows")

// Run reads the CSV and publishes its events. On error, Result says how far it got.
func Run(ctx context.Context, pub kafka.Publisher, src io.Reader, o Options) (Result, error) {
	res := Result{Errors: []RowError{}}
	cr := csv.NewReader(src)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	cr.ReuseRecord = true
	header, err := cr.Read()
	if err != nil {
		return res, fmt.Errorf("%w: the file is empty or not a CSV", ErrBadHeader)
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
	res.Columns = cols
	if len(bad) > 0 {
		return res, fmt.Errorf("%w: rename these columns to letters, digits and _ (starting with a letter): %s", ErrBadHeader, strings.Join(bad, ", "))
	}
	if idCol < 0 && emailCol < 0 {
		return res, fmt.Errorf("%w: the file needs an external_id or email column", ErrBadHeader)
	}

	var recs []*kgo.Record
	flush := func() error {
		if len(recs) > 0 {
			if err := pub.Publish(ctx, recs...); err != nil {
				return err
			}
			recs = recs[:0]
		}
		if o.Progress != nil {
			return o.Progress(res)
		}
		return nil
	}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		res.Rows++
		line := res.Rows + 1 // 1-based, after the header
		if o.MaxRows > 0 && res.Rows > o.MaxRows {
			return res, ErrTooManyRows
		}
		if err != nil {
			res.Errors = appendErr(res.Errors, line, "can't be read as CSV: "+err.Error())
			continue
		}
		krs, rowErr := rowEvents(cols, idCol, emailCol, rec, o)
		if rowErr != nil {
			res.Errors = appendErr(res.Errors, line, rowErr.Error())
			continue
		}
		recs = append(recs, krs...)
		res.Imported++
		if len(recs) >= batch {
			if err := flush(); err != nil {
				return res, err
			}
		}
	}
	return res, flush()
}

func rowEvents(cols []string, idCol, emailCol int, rec []string, o Options) ([]*kgo.Record, error) {
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
		return nil, errors.New("no external_id or email")
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
			v = NormalizePhone(v)
		}
		attrs[c] = v
	}
	if err := CoerceAttrs(attrs, o.Types); err != nil {
		return nil, err
	}
	evs := []event.Event{{ID: fmt.Sprintf("imp:%s:%s", o.ImportID, ext), TenantID: o.Tenant, ExternalID: ext, Name: event.Identify, Email: email, Attrs: attrs}}
	if o.ListID != 0 {
		evs = append(evs, event.Event{ID: fmt.Sprintf("imp:%s:%s:list", o.ImportID, ext), TenantID: o.Tenant, ExternalID: ext,
			Name: event.ListJoined, Props: map[string]any{"list_id": o.ListID, "source": "import"}})
	}
	krs := make([]*kgo.Record, 0, len(evs))
	for i := range evs {
		if err := evs[i].Normalize(o.Now); err != nil {
			return nil, err
		}
		kr, err := kafka.JSON(event.TopicRaw, evs[i].RawKey(), evs[i])
		if err != nil {
			return nil, err
		}
		krs = append(krs, kr)
	}
	return krs, nil
}

func appendErr(errs []RowError, row int64, msg string) []RowError {
	if len(errs) < MaxErrors {
		errs = append(errs, RowError{Row: row, Error: msg})
	}
	return errs
}

// NormalizePhone strips spaces, dashes and brackets so numbers compare as E.164.
func NormalizePhone(p string) string {
	return strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(p)
}

// CoerceAttrs converts imported text to the defined types, so "42" becomes a number and "yes" a
// boolean. A value that doesn't fit its type is an error for the row.
func CoerceAttrs(attrs map[string]any, types map[string]string) error {
	for k, v := range attrs {
		s, ok := v.(string)
		if !ok {
			continue
		}
		switch types[k] {
		case "number":
			f, ok := attr.Number(s)
			if !ok {
				return fmt.Errorf("%s: %q is not a number", k, s)
			}
			attrs[k] = f
		case "boolean":
			switch strings.ToLower(s) {
			case "true", "yes", "1", "y":
				attrs[k] = true
			case "false", "no", "0", "n":
				attrs[k] = false
			default:
				return fmt.Errorf("%s: %q is not yes/no", k, s)
			}
		}
	}
	return nil
}
