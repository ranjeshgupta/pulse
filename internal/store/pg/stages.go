package pg

// Lifecycle stages, companies (B2B accounts) and duplicate contacts.
//
// A contact's stage and company live on its profile (stage, company_id, company_name and
// company_<key> attributes) and change only through events the profile role applies; these tables
// hold the definitions the API validates against and copies from.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"pulse/internal/model"
)

// ErrInvalid is returned for a request the store can't carry out as asked.
var ErrInvalid = errors.New("invalid request")

func likeEscape(q string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(q))
}

// Stage is one step of the workspace's lifecycle, in SortOrder. MinPoints, when set, moves a
// contact forward to this stage when a points change reaches it.
type Stage struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	SortOrder   int       `json:"sort_order"`
	MinPoints   *int      `json:"min_points"`
	Contacts    int64     `json:"contacts"` // contacts in the stage now (the stage list only)
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const stageCols = `id, name, description, sort_order, min_points, created_at, updated_at`

func scanStage(r pgx.CollectableRow) (Stage, error) {
	var st Stage
	err := r.Scan(&st.ID, &st.Name, &st.Description, &st.SortOrder, &st.MinPoints, &st.CreatedAt, &st.UpdatedAt)
	return st, err
}

// ListStages returns the workspace's stages in order.
func (s *Store) ListStages(ctx context.Context, tenant int64) ([]Stage, error) {
	rows, err := s.db.Query(ctx, `SELECT `+stageCols+` FROM stages WHERE tenant_id = $1 ORDER BY sort_order, id`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanStage)
}

// StageCounts counts the contacts in each stage name (including names no stage has any more).
func (s *Store) StageCounts(ctx context.Context, tenant int64) (map[string]int64, error) {
	rows, err := s.db.Query(ctx, `
		SELECT attrs->>'stage', count(*) FROM contacts
		WHERE tenant_id = $1 AND attrs ? 'stage' AND NOT attrs ? 'merged_into' GROUP BY 1`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		counts[name] = n
	}
	return counts, rows.Err()
}

// CreateStage adds a stage after the last one.
func (s *Store) CreateStage(ctx context.Context, tenant int64, st Stage) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `
		INSERT INTO stages (tenant_id, name, description, min_points, sort_order)
		VALUES ($1, $2, $3, $4, (SELECT coalesce(max(sort_order) + 1, 0) FROM stages WHERE tenant_id = $1))
		RETURNING id`, tenant, st.Name, st.Description, st.MinPoints).Scan(&id)
	return id, conflict(err)
}

// UpdateStage changes a stage's name, description and threshold, returning its previous name.
func (s *Store) UpdateStage(ctx context.Context, tenant int64, st Stage) (string, error) {
	var old string
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM stages WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, st.ID).Scan(&old); err != nil {
			return notFound(err)
		}
		_, err := tx.Exec(ctx, `
			UPDATE stages SET name = $3, description = $4, min_points = $5, updated_at = now()
			WHERE tenant_id = $1 AND id = $2`, tenant, st.ID, st.Name, st.Description, st.MinPoints)
		return conflict(err)
	})
	return old, err
}

func (s *Store) DeleteStage(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM stages WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// ReorderStages puts the workspace's stages in the order of ids, which must name every stage once.
func (s *Store) ReorderStages(ctx context.Context, tenant int64, ids []int64) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM stages WHERE tenant_id = $1 AND id = ANY($2)`, tenant, ids).Scan(&n); err != nil {
			return err
		}
		var total int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM stages WHERE tenant_id = $1`, tenant).Scan(&total); err != nil {
			return err
		}
		if n != len(ids) || total != len(ids) {
			return fmt.Errorf("%w: list every stage of the workspace once", ErrInvalid)
		}
		_, err := tx.Exec(ctx, `
			UPDATE stages s SET sort_order = o.ord - 1, updated_at = now()
			FROM unnest($2::bigint[]) WITH ORDINALITY AS o(id, ord)
			WHERE s.tenant_id = $1 AND s.id = o.id`, tenant, ids)
		return err
	})
}

// StageRules loads every tenant's stages in order, for the profile role.
func (s *Store) StageRules(ctx context.Context) (map[int64][]Stage, error) {
	rows, err := s.db.Query(ctx, `SELECT tenant_id, name, sort_order, min_points FROM stages ORDER BY tenant_id, sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]Stage{}
	for rows.Next() {
		var t int64
		var st Stage
		if err := rows.Scan(&t, &st.Name, &st.SortOrder, &st.MinPoints); err != nil {
			return nil, err
		}
		out[t] = append(out[t], st)
	}
	return out, rows.Err()
}

// ---- companies ----

// Company is a B2B account. Its attributes are copied to each linked contact as company_<key>.
type Company struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Domain    string         `json:"domain"`
	Attrs     map[string]any `json:"attrs"`
	Members   int64          `json:"members"` // linked contacts (lists and detail only)
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

const companyCols = `id, name, domain, attrs, created_at, updated_at`

func scanCompany(r pgx.CollectableRow) (Company, error) {
	var c Company
	err := r.Scan(&c.ID, &c.Name, &c.Domain, &c.Attrs, &c.CreatedAt, &c.UpdatedAt)
	if c.Attrs == nil {
		c.Attrs = map[string]any{}
	}
	return c, err
}

// ListCompanies returns companies by name, optionally those whose name or domain contains q.
func (s *Store) ListCompanies(ctx context.Context, tenant int64, q string, offset, limit int) ([]Company, error) {
	like := "%" + likeEscape(q) + "%"
	rows, err := s.db.Query(ctx, `
		SELECT `+companyCols+` FROM companies
		WHERE tenant_id = $1 AND ($2 = '%%' OR name ILIKE $2 OR domain ILIKE $2)
		ORDER BY lower(name), id OFFSET $3 LIMIT $4`, tenant, like, offset, limit)
	if err != nil {
		return nil, err
	}
	cs, err := pgx.CollectRows(rows, scanCompany)
	if err != nil || len(cs) == 0 {
		return cs, err
	}
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = strconv.FormatInt(c.ID, 10)
	}
	rows, err = s.db.Query(ctx, `
		SELECT attrs->>'company_id', count(*) FROM contacts
		WHERE tenant_id = $1 AND attrs ? 'company_id' AND attrs->>'company_id' = ANY($2) AND NOT attrs ? 'merged_into'
		GROUP BY 1`, tenant, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		counts[id] = n
	}
	for i := range cs {
		cs[i].Members = counts[ids[i]]
	}
	return cs, rows.Err()
}

func (s *Store) GetCompany(ctx context.Context, tenant, id int64) (Company, error) {
	rows, err := s.db.Query(ctx, `SELECT `+companyCols+` FROM companies WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err != nil {
		return Company{}, err
	}
	c, err := pgx.CollectOneRow(rows, scanCompany)
	if err != nil {
		return c, notFound(err)
	}
	err = s.db.QueryRow(ctx, `
		SELECT count(*) FROM contacts
		WHERE tenant_id = $1 AND attrs ? 'company_id' AND attrs->>'company_id' = $2 AND NOT attrs ? 'merged_into'`,
		tenant, strconv.FormatInt(id, 10)).Scan(&c.Members)
	return c, err
}

func (s *Store) CreateCompany(ctx context.Context, tenant int64, c Company) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `
		INSERT INTO companies (tenant_id, name, domain, attrs) VALUES ($1, $2, $3, $4) RETURNING id`,
		tenant, c.Name, c.Domain, c.Attrs).Scan(&id)
	return id, conflict(err)
}

func (s *Store) UpdateCompany(ctx context.Context, tenant int64, c Company) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE companies SET name = $3, domain = $4, attrs = $5, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
		tenant, c.ID, c.Name, c.Domain, c.Attrs)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return conflict(err)
}

func (s *Store) DeleteCompany(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM companies WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// UpsertCompanyByName returns the company with this name (any letter case), creating it if needed.
func (s *Store) UpsertCompanyByName(ctx context.Context, tenant int64, name string) (Company, error) {
	rows, err := s.db.Query(ctx, `
		INSERT INTO companies (tenant_id, name) VALUES ($1, $2)
		ON CONFLICT (tenant_id, lower(name)) DO UPDATE SET name = companies.name
		RETURNING `+companyCols, tenant, name)
	if err != nil {
		return Company{}, err
	}
	return pgx.CollectOneRow(rows, scanCompany)
}

// CompanyMembers returns up to limit contacts linked to the company.
func (s *Store) CompanyMembers(ctx context.Context, tenant, id int64, limit int) ([]model.Contact, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+contactCols+` FROM contacts
		WHERE tenant_id = $1 AND attrs ? 'company_id' AND attrs->>'company_id' = $2 AND NOT attrs ? 'merged_into'
		ORDER BY id LIMIT $3`, tenant, strconv.FormatInt(id, 10), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Contact, error) { return scanContact(r) })
}

// MemberIDs pages through the external ids of contacts whose company_id or stage attribute is
// value, after contact id after, for fanning a change out to them.
func (s *Store) MemberIDs(ctx context.Context, tenant int64, key, value string, after uint32, limit int) ([]string, uint32, error) {
	if key != "company_id" && key != "stage" {
		return nil, 0, fmt.Errorf("%w: members by %q", ErrInvalid, key)
	}
	// key is one of two constants, so it is safe in the SQL text (and matches the indexes).
	rows, err := s.db.Query(ctx, `
		SELECT id, external_id FROM contacts
		WHERE tenant_id = $1 AND attrs ? '`+key+`' AND attrs->>'`+key+`' = $2 AND id > $3 AND NOT attrs ? 'merged_into'
		ORDER BY id LIMIT $4`, tenant, value, int64(after), limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var exts []string
	var last int64
	for rows.Next() {
		var ext string
		if err := rows.Scan(&last, &ext); err != nil {
			return nil, 0, err
		}
		exts = append(exts, ext)
	}
	return exts, uint32(last), rows.Err()
}

// ---- duplicates ----

// DuplicateGroup is a set of contacts sharing an email address or phone number. Total counts them
// all; Contacts holds the first few.
type DuplicateGroup struct {
	Value    string          `json:"value"`
	Total    int             `json:"total"`
	Contacts []model.Contact `json:"contacts"`
}

// DuplicateContacts finds up to limit groups of contacts (not merged away) that share an email
// address (by "email", ignoring letter case) or a phone number (by "phone"), with up to perGroup
// contacts each, oldest first.
func (s *Store) DuplicateContacts(ctx context.Context, tenant int64, by string, limit, perGroup int) ([]DuplicateGroup, error) {
	var expr, has string
	switch by {
	case "email":
		expr, has = "lower(email)", "email <> ''"
	case "phone":
		expr, has = "attrs->>'phone'", "attrs ? 'phone' AND attrs->>'phone' <> ''"
	default:
		return nil, fmt.Errorf("%w: duplicates by %q", ErrInvalid, by)
	}
	// expr and has are constants chosen above.
	rows, err := s.db.Query(ctx, `
		WITH d AS (
			SELECT `+expr+` AS v FROM contacts
			WHERE tenant_id = $1 AND `+has+` AND NOT attrs ? 'merged_into'
			GROUP BY 1 HAVING count(*) > 1 ORDER BY 1 LIMIT $2
		)
		SELECT v, n, tenant_id, id, external_id, email, attrs, created_at, updated_at FROM (
			SELECT d.v, count(*) OVER (PARTITION BY d.v) AS n, row_number() OVER (PARTITION BY d.v ORDER BY c.id) AS rn, c.*
			FROM d JOIN contacts c ON c.tenant_id = $1 AND `+expr+` = d.v AND NOT c.attrs ? 'merged_into'
		) x WHERE rn <= $3 ORDER BY v, id`, tenant, limit, perGroup)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DuplicateGroup
	for rows.Next() {
		var v string
		var n, id int64
		var c model.Contact
		if err := rows.Scan(&v, &n, &c.TenantID, &id, &c.ExternalID, &c.Email, &c.Attrs, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.ID = uint32(id)
		if len(out) == 0 || out[len(out)-1].Value != v {
			out = append(out, DuplicateGroup{Value: v, Total: int(n)})
		}
		g := &out[len(out)-1]
		g.Contacts = append(g.Contacts, c)
	}
	return out, rows.Err()
}
