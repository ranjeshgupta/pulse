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
}

func (s *Server) roleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/roles", s.authed(s.listRoles))
	mux.HandleFunc("POST /v1/roles", s.admin(s.saveRole))
	mux.HandleFunc("PUT /v1/roles/{id}", s.admin(s.saveRole))
	mux.HandleFunc("DELETE /v1/roles/{id}", s.admin(s.deleteRole))
	mux.HandleFunc("PUT /v1/users/{id}/role", s.admin(s.setUserRole))
}

func (s *Server) roles() RoleStore { return s.Store.(RoleStore) }

// validRole reports whether role can be given to a user: built-in or a custom role of the workspace.
func (s *Server) validRole(ctx context.Context, tenant int64, role string) (bool, error) {
	if auth.IsBuiltin(role) {
		return true, nil
	}
	rs, ok := s.Store.(RoleStore)
	if !ok {
		return false, nil
	}
	return rs.RoleExists(ctx, tenant, role)
}

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
	if ok, err := s.validRole(r.Context(), p.TenantID, req.Role); err != nil {
		s.fail(w, err)
		return
	} else if !ok {
		writeErr(w, http.StatusBadRequest, "choose a role")
		return
	}
	if err := s.roles().SetUserRole(r.Context(), p.TenantID, id, req.Role); err != nil {
		s.failConflict(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "role": req.Role})
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
