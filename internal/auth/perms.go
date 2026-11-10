package auth

// Permissions: a role gives a level per area. Built-in roles are admin, editor and viewer; a
// workspace can add custom roles (stored as "role:<id>" on the user).

import (
	"fmt"
	"strings"
)

// Areas of the app a role can be given access to.
const (
	AreaContacts   = "contacts"   // profiles, events in, tags, points, list members, imports, exports
	AreaSegments   = "segments"   // segments, lists, attribute definitions, scoring rules
	AreaJourneys   = "journeys"   // journeys; publish = activate and pause
	AreaBroadcasts = "broadcasts" // broadcasts; publish = schedule, cancel, pick A/B winners
	AreaContent    = "content"    // templates, focus items, web content, forms, test sends
	AreaReports    = "reports"    // dashboard, reports, funnels (edit = save funnels)
	AreaChannels   = "channels"   // providers and the test inbox
	AreaSettings   = "settings"   // team, roles, SSO, keys, delivery rules, webhooks, audit log
)

var Areas = []string{AreaContacts, AreaSegments, AreaJourneys, AreaBroadcasts, AreaContent, AreaReports, AreaChannels, AreaSettings}

// Level is how much of an area a role may do. Each level includes the ones below it.
type Level int

const (
	None    Level = 0
	View    Level = 1
	Edit    Level = 2
	Publish Level = 3 // journeys and broadcasts: go live; elsewhere the same as Edit
)

var levelNames = map[string]Level{"none": None, "view": View, "edit": Edit, "publish": Publish}

func (l Level) String() string {
	for n, v := range levelNames {
		if v == l {
			return n
		}
	}
	return "none"
}

func (l Level) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

func (l *Level) UnmarshalText(b []byte) error {
	v, ok := levelNames[strings.ToLower(string(b))]
	if !ok {
		return fmt.Errorf("unknown access level %q (none, view, edit or publish)", b)
	}
	*l = v
	return nil
}

// Perms maps each area to a level; missing areas are None.
type Perms map[string]Level

// Allows reports whether the perms reach level in area.
func (p Perms) Allows(area string, level Level) bool { return p[area] >= level }

// Validate checks that every area is known.
func (p Perms) Validate() error {
	for a := range p {
		known := false
		for _, k := range Areas {
			known = known || k == a
		}
		if !known {
			return fmt.Errorf("unknown area %q", a)
		}
	}
	return nil
}

const RoleViewer = "viewer"

// CustomRolePrefix marks a user's role as a workspace role: "role:12".
const CustomRolePrefix = "role:"

// BuiltinPerms returns the permissions of a built-in role, and whether role is one.
func BuiltinPerms(role string) (Perms, bool) {
	all := func(l Level) Perms {
		p := Perms{}
		for _, a := range Areas {
			p[a] = l
		}
		return p
	}
	switch role {
	case RoleAdmin:
		return all(Publish), true
	case RoleEditor:
		p := all(Publish)
		p[AreaChannels], p[AreaSettings] = View, View
		return p, true
	case RoleViewer:
		p := all(View)
		p[AreaSettings] = None
		return p, true
	}
	return nil, false
}

// IsBuiltin reports whether role names a built-in role.
func IsBuiltin(role string) bool {
	_, ok := BuiltinPerms(role)
	return ok
}
