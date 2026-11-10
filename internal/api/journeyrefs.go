package api

// Checks of what a journey refers to that only the workspace knows: stages, users, other journeys,
// focus items, companies, email templates; and who may save a journey that deletes contacts, removes
// Do Not Contact or changes owners and companies.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"pulse/internal/auth"
	"pulse/internal/channel"
	"pulse/internal/journey"
	"pulse/internal/model"
	"pulse/internal/store/pg"
)

// JourneyRefStore is what the reference checks read (Postgres).
type JourneyRefStore interface {
	JourneyIDs(ctx context.Context, tenant int64, ids []int64) (map[int64]bool, error)
	GetCompany(ctx context.Context, tenant, id int64) (pg.Company, error)
}

// contactSteps change consent or a contact's CRM data beyond what marketing steps do (tags, points,
// attributes): saving or activating a journey with them needs edit access to contacts, as doing
// the same by hand would. Adding Do Not Contact is left out: it only ever stops messages.
var contactSteps = map[string]string{
	journey.NodeDeleteContact: "deletes contacts",
	journey.NodeDNCRemove:     "removes Do Not Contact",
	journey.NodeUpdateOwner:   "changes contact owners",
	journey.NodeUpdateCompany: "changes companies",
	journey.NodeAddCompany:    "changes contacts' companies",
	journey.NodeCompanyPoints: "changes company scores",
}

// deletePermOK refuses a journey with a step from contactSteps from someone who can't edit contacts.
func deletePermOK(w http.ResponseWriter, p model.Principal, g journey.Graph) bool {
	if p.Perms.Allows(auth.AreaContacts, auth.Edit) {
		return true
	}
	for _, t := range []string{journey.NodeDeleteContact, journey.NodeDNCRemove, journey.NodeUpdateOwner,
		journey.NodeUpdateCompany, journey.NodeAddCompany, journey.NodeCompanyPoints} {
		if g.HasType(t) {
			writeErr(w, http.StatusForbidden, "a journey that "+contactSteps[t]+" needs edit access to contacts")
			return false
		}
	}
	return true
}

// journeyRefsOK checks a journey's references; self is the journey's own id (0 when new).
func (s *Server) journeyRefsOK(w http.ResponseWriter, r *http.Request, p model.Principal, self int64, graph json.RawMessage) bool {
	g, err := journey.ParseGraph(graph)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	if !deletePermOK(w, p, g) {
		return false
	}
	err = s.checkJourneyStages(r.Context(), p.TenantID, graph)
	if err == nil {
		err = s.checkJourneyRefs(r.Context(), p.TenantID, self, g)
	}
	if errors.Is(err, errBadJourney) {
		writeErr(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), errBadJourney.Error()+": "))
		return false
	}
	if err != nil {
		s.fail(w, err)
		return false
	}
	return true
}

func (s *Server) checkJourneyRefs(ctx context.Context, tenant, self int64, g journey.Graph) error {
	bad := func(id, format string, a ...any) error {
		return fmt.Errorf("%w: step %q: %s", errBadJourney, id, fmt.Sprintf(format, a...))
	}
	var users map[int64]bool
	userOK := func(uid int64) (bool, error) {
		if users == nil {
			us, err := s.Store.ListUsers(ctx, tenant)
			if err != nil {
				return false, err
			}
			users = map[int64]bool{}
			for _, u := range us {
				users[u.ID] = true
			}
		}
		return users[uid], nil
	}
	refs, _ := s.Store.(JourneyRefStore)
	for id, n := range g.Nodes {
		switch n.Type {
		case journey.NodeNotifyUser, journey.NodeUpdateOwner:
			ids := n.UserIDs
			if n.Type == journey.NodeUpdateOwner && *n.OwnerID > 0 {
				ids = []int64{*n.OwnerID}
			}
			for _, uid := range ids {
				ok, err := userOK(uid)
				if err != nil {
					return err
				}
				if !ok {
					return bad(id, "there is no user %d in this workspace", uid)
				}
			}
			if tg, ok := s.Store.(templateGetter); ok && n.Type == journey.NodeNotifyUser {
				t, err := tg.Template(ctx, tenant, n.TemplateID)
				if errors.Is(err, pg.ErrNotFound) || err == nil && t.TenantID != tenant {
					return bad(id, "template %d doesn't exist", n.TemplateID)
				} else if err != nil {
					return err
				}
				if t.Channel != channel.Email && t.Channel != "" {
					return bad(id, "choose an email template")
				}
			}
		case journey.NodeIfJourney, journey.NodeChangeJourney:
			ids := append(append(append([]int64{}, n.JourneyIDs...), n.AddJourneys...), n.RemoveJourneys...)
			for _, j := range ids {
				if self != 0 && j == self {
					return bad(id, "choose other journeys, not this one")
				}
			}
			if refs != nil {
				have, err := refs.JourneyIDs(ctx, tenant, ids)
				if err != nil {
					return err
				}
				for _, j := range ids {
					if !have[j] {
						return bad(id, "journey %d doesn't exist", j)
					}
				}
			}
		case journey.NodeShowFocus:
			os, ok := s.Store.(OnsiteStore)
			if !ok {
				continue
			}
			if _, err := os.GetFocusItem(ctx, tenant, n.FocusID); errors.Is(err, pg.ErrNotFound) {
				return bad(id, "focus item %d doesn't exist", n.FocusID)
			} else if err != nil {
				return err
			}
		case journey.NodeAddCompany:
			if refs != nil {
				if _, err := refs.GetCompany(ctx, tenant, n.CompanyID); errors.Is(err, pg.ErrNotFound) {
					return bad(id, "company %d doesn't exist", n.CompanyID)
				} else if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type templateGetter interface {
	Template(ctx context.Context, tenant, id int64) (model.Template, error)
}
