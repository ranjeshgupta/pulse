package api

// Custom roles, and changing a team member's role.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"pulse/internal/auth"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

type RoleStore interface {
	ListRoles(ctx context.Context, tenant int64) ([]model.Role, error)
	RoleExists(ctx context.Context, tenant int64, role string) (bool, error)
	SaveRole(ctx context.Context, tenant int64, r model.Role) (int64, error)
	DeleteRole(ctx context.Context, tenant, id int64) error
	SetUserRole(ctx context.Context, tenant, user int64, role string) error
	RolePerms(ctx context.Context, tenant int64, role string) (auth.Perms, bool, error)
}

var (
	errNoSuchRole   = errors.New("choose a role: admin, editor, viewer or a custom role")
	errBeyondCaller = errors.New("you can only give, change or remove access you have yourself")
)

// grantable checks that role exists and that p holds at least its access. It is used for every
// way a role reaches a person (invite, role change, SSO default) and for changes to roles and
// people who hold them, so nobody can raise their own access through a colleague or a new account.
func (s *Server) grantable(ctx context.Context, p model.Principal, role string) error {
	rs, ok := s.Store.(RoleStore)
	if !ok {
		if auth.IsBuiltin(role) && p.Perms.Covers(mustBuiltin(role)) {
			return nil
		}
		return errNoSuchRole
	}
	perms, exists, err := rs.RolePerms(ctx, p.TenantID, role)
	switch {
	case err != nil:
		return err
	case !exists:
		return errNoSuchRole
	case !p.Perms.Covers(perms):
		return errBeyondCaller
	}
	return nil
}

func mustBuiltin(role string) auth.Perms {
	p, _ := auth.BuiltinPerms(role)
	return p
}

// failGrant answers a grantable error.
func (s *Server) failGrant(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoSuchRole):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errBeyondCaller):
		writeErr(w, http.StatusForbidden, err.Error())
	default:
		s.fail(w, err)
	}
}

func (s *Server) roleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/roles", s.authed(s.listRoles))
	mux.HandleFunc("POST /v1/roles", s.admin(s.saveRole))
	mux.HandleFunc("PUT /v1/roles/{id}", s.admin(s.saveRole))
	mux.HandleFunc("DELETE /v1/roles/{id}", s.admin(s.deleteRole))
	mux.HandleFunc("PUT /v1/users/{id}/role", s.admin(s.setUserRole))
}

func (s *Server) roles() RoleStore { return s.Store.(RoleStore) }

// builtinRoles describes the built-in roles for the app.
func builtinRoles() []map[string]any {
	var out []map[string]any
	for _, r := range []struct{ key, name, desc string }{
		{auth.RoleAdmin, "Admin", "Everything, including team, sign-in, keys and channels."},
		{auth.RoleEditor, "Editor", "Builds and launches everything; can see but not change settings and channels."},
		{auth.RoleViewer, "Viewer", "Sees everything except settings; changes nothing."},
	} {
		p, _ := auth.BuiltinPerms(r.key)
		out = append(out, map[string]any{"key": r.key, "name": r.name, "description": r.desc, "permissions": p})
	}
	return out
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request, p model.Principal) {
	custom, err := s.roles().ListRoles(r.Context(), p.TenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if custom == nil {
		custom = []model.Role{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"builtin": builtinRoles(), "custom": custom, "areas": auth.Areas})
}

func (s *Server) saveRole(w http.ResponseWriter, r *http.Request, p model.Principal) {
	var ro model.Role
	if !decode(w, r, &ro) {
		return
	}
	ro.ID = 0
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		ro.ID = id
	}
	ro.Name = strings.TrimSpace(ro.Name)
	if ro.Name == "" || len(ro.Name) > 60 {
		writeErr(w, http.StatusBadRequest, "name the role (up to 60 characters)")
		return
	}
	if ro.Permissions == nil {
		ro.Permissions = auth.Perms{}
	}
	if err := ro.Permissions.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !p.Perms.Covers(ro.Permissions) {
		s.failGrant(w, errBeyondCaller)
		return
	}
	if ro.ID != 0 {
		key := auth.CustomRolePrefix + strconv.FormatInt(ro.ID, 10)
		if key == p.Role {
			writeErr(w, http.StatusForbidden, "you can't change the role you have; ask another admin")
			return
		}
		if err := s.grantable(r.Context(), p, key); err != nil { // the role as it is now
			s.failGrant(w, err)
			return
		}
	}
	id, err := s.roles().SaveRole(r.Context(), p.TenantID, ro)
	if err != nil {
		s.failConflict(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "key": auth.CustomRolePrefix + strconv.FormatInt(id, 10)})
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.grantable(r.Context(), p, auth.CustomRolePrefix+strconv.FormatInt(id, 10)); err != nil && !errors.Is(err, errNoSuchRole) {
		s.failGrant(w, err)
		return
	}
	if err := s.roles().DeleteRole(r.Context(), p.TenantID, id); err != nil {
		s.failConflict(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setUserRole(w http.ResponseWriter, r *http.Request, p model.Principal) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &req) {
		return
	}
	if id == p.UserID {
		writeErr(w, http.StatusBadRequest, "you can't change your own role")
		return
	}
	if err := s.grantable(r.Context(), p, req.Role); err != nil {
		s.failGrant(w, err)
		return
	}
	if err := s.canManageUser(r.Context(), p, id); err != nil {
		s.failGrant(w, err)
		return
	}
	if err := s.roles().SetUserRole(r.Context(), p.TenantID, id, req.Role); err != nil {
		s.failConflict(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "role": req.Role})
}

// canManageUser checks that p holds at least the access of the user they're changing or removing.
func (s *Server) canManageUser(ctx context.Context, p model.Principal, user int64) error {
	u, err := s.Store.User(ctx, p.TenantID, user)
	if err != nil {
		return err
	}
	err = s.grantable(ctx, p, u.Role)
	if errors.Is(err, errNoSuchRole) {
		return nil // a role that was deleted grants nothing
	}
	return err
}

// failConflict reports store messages meant for people (409) and everything else as usual.
func (s *Server) failConflict(w http.ResponseWriter, err error) {
	if errors.Is(err, pg.ErrNotFound) {
		s.fail(w, err)
		return
	}
	if pg.IsUserError(err) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.fail(w, err)
}
