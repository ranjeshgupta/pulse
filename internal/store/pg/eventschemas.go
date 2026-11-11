package pg

// Event schemas (see internal/eventschema).

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"pulse/internal/eventschema"
)

const eventSchemaCols = `name, label, description, mode, props, updated_at`

func scanEventSchema(r pgx.CollectableRow) (eventschema.Schema, error) {
	var s eventschema.Schema
	var props []byte
	if err := r.Scan(&s.Name, &s.Label, &s.Description, &s.Mode, &props, &s.UpdatedAt); err != nil {
		return s, err
	}
	err := json.Unmarshal(props, &s.Props)
	return s, err
}

func (s *Store) ListEventSchemas(ctx context.Context, tenant int64) ([]eventschema.Schema, error) {
	rows, err := s.db.Query(ctx, `SELECT `+eventSchemaCols+` FROM event_schemas WHERE tenant_id = $1 ORDER BY name`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanEventSchema)
}

func (s *Store) GetEventSchema(ctx context.Context, tenant int64, name string) (eventschema.Schema, error) {
	rows, err := s.db.Query(ctx, `SELECT `+eventSchemaCols+` FROM event_schemas WHERE tenant_id = $1 AND name = $2`, tenant, name)
	if err != nil {
		return eventschema.Schema{}, err
	}
	list, err := pgx.CollectRows(rows, scanEventSchema)
	if err != nil {
		return eventschema.Schema{}, err
	}
	if len(list) == 0 {
		return eventschema.Schema{}, ErrNotFound
	}
	return list[0], nil
}

// PutEventSchema creates or replaces an event's schema.
func (s *Store) PutEventSchema(ctx context.Context, tenant int64, sc eventschema.Schema) error {
	props, err := json.Marshal(sc.Props)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO event_schemas (tenant_id, name, label, description, mode, props) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, name) DO UPDATE SET label = EXCLUDED.label, description = EXCLUDED.description,
			mode = EXCLUDED.mode, props = EXCLUDED.props, updated_at = now()`,
		tenant, sc.Name, sc.Label, sc.Description, sc.Mode, props)
	return err
}

func (s *Store) DeleteEventSchema(ctx context.Context, tenant int64, name string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM event_schemas WHERE tenant_id = $1 AND name = $2`, tenant, name)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// AllEventSchemas loads every workspace's schemas for the roles that check events.
func (s *Store) AllEventSchemas(ctx context.Context) (map[int64][]eventschema.Schema, error) {
	rows, err := s.db.Query(ctx, `SELECT tenant_id, `+eventSchemaCols+` FROM event_schemas`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]eventschema.Schema{}
	for rows.Next() {
		var tenant int64
		var sc eventschema.Schema
		var props []byte
		if err := rows.Scan(&tenant, &sc.Name, &sc.Label, &sc.Description, &sc.Mode, &props, &sc.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(props, &sc.Props); err != nil {
			continue
		}
		out[tenant] = append(out[tenant], sc)
	}
	return out, rows.Err()
}
