package pg

// Custom roles.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"pulse/internal/auth"
	"pulse/internal/model"
)

// rolePerms resolves a user's role: built-in, or the custom role's stored permissions. A custom
// role that was deleted (or never existed) gives no access.
func rolePerms(role string, custom []byte) auth.Perms {
	if p, ok := auth.BuiltinPerms(role); ok {
		return p
	}
	p := auth.Perms{}
	if len(custom) > 0 {
		_ = json.Unmarshal(custom, &p)
	}
	return p
}

func (s *Store) ListRoles(ctx context.Context, tenant int64) ([]model.Role, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.name, r.description, r.permissions, r.updated_at,
			(SELECT count(*) FROM users u WHERE u.tenant_id = r.tenant_id AND u.role = 'role:' || r.id)
		FROM roles r WHERE r.tenant_id = $1 ORDER BY r.name`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Role, error) {
		var ro model.Role
		var perms []byte
		err := r.Scan(&ro.ID, &ro.Name, &ro.Description, &perms, &ro.UpdatedAt, &ro.Users)
		if err == nil {
			ro.Permissions = rolePerms("", perms)
		}
		return ro, err
	})
}

// RoleExists reports whether role ("role:12") is a custom role of the workspace.
func (s *Store) RoleExists(ctx context.Context, tenant int64, role string) (bool, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(role, auth.CustomRolePrefix), 10, 64)
	if !strings.HasPrefix(role, auth.CustomRolePrefix) || err != nil {
		return false, nil
	}
	var ok bool
	err = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roles WHERE tenant_id = $1 AND id = $2)`, tenant, id).Scan(&ok)
	return ok, err
}

// SaveRole creates (ID 0) or updates a custom role.
func (s *Store) SaveRole(ctx context.Context, tenant int64, r model.Role) (int64, error) {
	perms, err := json.Marshal(r.Permissions)
	if err != nil {
		return 0, err
	}
	if r.ID == 0 {
		err = s.db.QueryRow(ctx, `INSERT INTO roles (tenant_id, name, description, permissions) VALUES ($1, $2, $3, $4) RETURNING id`,
			tenant, r.Name, r.Description, perms).Scan(&r.ID)
	} else {
		tag, e := s.db.Exec(ctx, `UPDATE roles SET name = $3, description = $4, permissions = $5, updated_at = now() WHERE tenant_id = $1 AND id = $2`,
			tenant, r.ID, r.Name, r.Description, perms)
		err = e
		if err == nil && tag.RowsAffected() == 0 {
			err = ErrNotFound
		}
	}
	if errors.Is(conflict(err), ErrConflict) {
		return 0, errorString("a role with this name already exists")
	}
	return r.ID, err
}

// DeleteRole removes a custom role nobody holds.
func (s *Store) DeleteRole(ctx context.Context, tenant, id int64) error {
	var users int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM users WHERE tenant_id = $1 AND role = $2`, tenant, auth.CustomRolePrefix+strconv.FormatInt(id, 10)).Scan(&users); err != nil {
		return err
	}
	if users > 0 {
		return errorString("people still have this role; give them another role first")
	}
	tag, err := s.db.Exec(ctx, `DELETE FROM roles WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// SetUserRole changes a user's role. The last admin can't be demoted.
func (s *Store) SetUserRole(ctx context.Context, tenant, user int64, role string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var cur string
		if err := tx.QueryRow(ctx, `SELECT role FROM users WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenant, user).Scan(&cur); err != nil {
			return notFound(err)
		}
		if cur == auth.RoleAdmin && role != auth.RoleAdmin {
			var admins int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE tenant_id = $1 AND role = 'admin'`, tenant).Scan(&admins); err != nil {
				return err
			}
			if admins <= 1 {
				return errorString("this is the workspace's only admin; make someone else an admin first")
			}
		}
		_, err := tx.Exec(ctx, `UPDATE users SET role = $3 WHERE tenant_id = $1 AND id = $2`, tenant, user, role)
		return err
	})
}
