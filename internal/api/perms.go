package api

// Route permissions: which area and level each signed-in route needs. Reads need view, changes
// need edit, and going live (journeys, broadcasts) needs publish. A route nobody listed needs
// settings edit, so a new route is never open to everyone by accident.

import (
	"net/http"
	"strings"

	"pulse/internal/auth"
)

// areaByPrefix maps the first path segment after /v1/ to an area.
var areaByPrefix = map[string]string{
	"contacts": auth.AreaContacts, "events": auth.AreaContacts, "tags": auth.AreaContacts, "imports": auth.AreaContacts,
	"devices": auth.AreaContacts, "companies": auth.AreaContacts,
	"segments": auth.AreaSegments, "lists": auth.AreaSegments, "attributes": auth.AreaSegments, "scoring": auth.AreaSegments,
	"stages":   auth.AreaSegments,
	"journeys": auth.AreaJourneys, "journey-steps": auth.AreaJourneys,
	"broadcasts": auth.AreaBroadcasts,
	"templates":  auth.AreaContent, "focus-items": auth.AreaContent, "dwc": auth.AreaContent, "forms": auth.AreaContent, "inapp": auth.AreaContent,
	"assets": auth.AreaContent, "landing-pages": auth.AreaContent, "test-contacts": auth.AreaContent,
	"stats": auth.AreaReports, "funnels": auth.AreaReports, "cohorts": auth.AreaReports, "reports": auth.AreaReports,
	"revenue": auth.AreaReports, "analytics": auth.AreaReports, // M4 dashboards
	"live":     auth.AreaContacts, // the live event feed shows people's events
	"channels": auth.AreaChannels, "sandbox": auth.AreaChannels,
	"settings": auth.AreaSettings, "users": auth.AreaSettings, "roles": auth.AreaSettings, "audit": auth.AreaSettings,
	"integrations": auth.AreaSettings, // audience links also need segments edit (checked in the handler)
}

// special lists routes whose need differs from the method rule.
var special = map[string]need{
	"GET /v1/auth/me":                                 {"", auth.None}, // everyone signed in
	"GET /v1/events/names":                            {auth.AreaReports, auth.View},
	"POST /v1/segments/preview":                       {auth.AreaSegments, auth.View},
	"POST /v1/templates/preview":                      {auth.AreaContent, auth.View},
	"POST /v1/landing-pages/preview":                  {auth.AreaContent, auth.View},
	"POST /v1/templates/text":                         {auth.AreaContent, auth.View},
	"GET /v1/templates/sample-event":                  {auth.AreaContacts, auth.View}, // a contact's real event
	"POST /v1/funnels/run":                            {auth.AreaReports, auth.View},
	"POST /v1/cohorts/run":                            {auth.AreaReports, auth.View},
	"POST /v1/reports/run":                            {auth.AreaReports, auth.View},
	"POST /v1/revenue/report":                         {auth.AreaReports, auth.View},
	"PUT /v1/revenue/settings":                        {auth.AreaSettings, auth.Edit}, // a workspace setting
	"POST /v1/channels/test":                          {auth.AreaContent, auth.Edit},  // a test send of a template
	"POST /v1/journeys/{id}/{action}":                 {auth.AreaJourneys, auth.Publish},
	"POST /v1/broadcasts/{id}/schedule":               {auth.AreaBroadcasts, auth.Publish},
	"POST /v1/broadcasts/{id}/cancel":                 {auth.AreaBroadcasts, auth.Publish},
	"POST /v1/broadcasts/{id}/runs/{run}/winner":      {auth.AreaBroadcasts, auth.Publish},
	"GET /v1/journeys/{id}/report":                    {auth.AreaReports, auth.View},
	"GET /v1/broadcasts/{id}/report":                  {auth.AreaReports, auth.View},
	"GET /v1/forms/{id}/submissions":                  {auth.AreaContent, auth.View},
	"GET /v1/forms/{id}/uploads/{upload}":             {auth.AreaContacts, auth.View}, // files contacts uploaded: personal data
	"GET /v1/contacts/export":                         {auth.AreaContacts, auth.Edit}, // a full copy of the data
	"GET /v1/contacts/{external_id}/preferences-link": {auth.AreaContacts, auth.Edit},
	"GET /v1/contacts/{external_id}/data":             {auth.AreaContacts, auth.Edit}, // everything about one person
	"GET /v1/settings/sso":                            {auth.AreaSettings, auth.Edit}, // shows SSO configuration
	"GET /v1/settings/identity":                       {auth.AreaSettings, auth.Edit}, // shows the signing secret
	"GET /v1/settings/webhooks":                       {auth.AreaSettings, auth.Edit}, // shows signing secrets
	"GET /v1/settings/webhooks/{id}":                  {auth.AreaSettings, auth.Edit},
	"GET /v1/settings/bounces":                        {auth.AreaSettings, auth.Edit}, // shows the bounce URL secret
	"GET /v1/settings/bounce-mailbox":                 {auth.AreaSettings, auth.Edit}, // mailbox server and user
	"POST /v1/sandbox/receipts":                       {auth.AreaContent, auth.Edit},  // test tools, like test sends
	"POST /v1/sandbox/bounce":                         {auth.AreaContent, auth.Edit},
	"POST /v1/sandbox/inbound":                        {auth.AreaContent, auth.Edit},
	"GET /v1/sandbox/messages":                        {auth.AreaContent, auth.View},
	"GET /v1/inapp/check":                             {auth.AreaContacts, auth.View}, // reveals a contact's segment matches
	"GET /v1/attributes/{key}/values":                 {auth.AreaContacts, auth.View}, // real values from contacts
	"GET /v1/events/{name}/values":                    {auth.AreaContacts, auth.View},
	"GET /v1/audit":                                   {auth.AreaSettings, auth.Edit},
	"GET /v1/audit/export":                            {auth.AreaSettings, auth.Edit}, // the same as viewing the log
	"GET /v1/channels/catalog":                        {auth.AreaChannels, auth.View},
	"GET /v1/integrations/{id}/fields":                {auth.AreaSettings, auth.Edit}, // calls the CRM with the stored credentials
	// The Campaigns hub lists broadcasts and journeys: the handler shows only what the role may view.
	"GET /v1/campaigns":          {"", auth.None},
	"GET /v1/campaigns/tags":     {"", auth.None},
	"POST /v1/segments/estimate": {auth.AreaSegments, auth.View}, // counts only, like a segment preview
	// M5 Settings → Automation & limits. Reading them needs settings view, like delivery rules and
	// data settings: nothing in them is secret, and editors should see why old data disappears.
	// Changing, previewing (it scans old data to count it) and running cleanup need settings edit.
	"GET /v1/settings/automation":                {auth.AreaSettings, auth.View},
	"PUT /v1/settings/automation":                {auth.AreaSettings, auth.Edit},
	"POST /v1/settings/automation/preview":       {auth.AreaSettings, auth.Edit},
	"POST /v1/settings/automation/run-retention": {auth.AreaSettings, auth.Edit},
	// M5 provider tools: they use the provider's credentials, so the handlers also need settings
	// edit (like saving a provider).
	"POST /v1/channels/providers/check-auth": {auth.AreaChannels, auth.Edit},
	"POST /v1/channels/providers/preview":    {auth.AreaChannels, auth.Edit},
	// M6 AMP: checking and previewing AMP code is like a template preview (saving needs content
	// edit, the template routes' rule). Overlap counts are counts only, like a segment estimate.
	"POST /v1/templates/amp/check": {auth.AreaContent, auth.View},
	"POST /v1/dwc/overlap":         {auth.AreaContent, auth.View},
	"POST /v1/focus-items/overlap": {auth.AreaContent, auth.View},
	// M6 web push: the prompt and the install check belong to the web push channel. The check
	// makes the server fetch an address, so it needs edit like saving the settings.
	"GET /v1/settings/web-push":              {auth.AreaChannels, auth.View},
	"PUT /v1/settings/web-push":              {auth.AreaChannels, auth.Edit},
	"POST /v1/settings/web-push/check":       {auth.AreaChannels, auth.Edit},
	"POST /v1/settings/web-push/check-token": {auth.AreaChannels, auth.Edit},
	"GET /v1/settings/web-push/diagnosis":    {auth.AreaChannels, auth.View},
}

type need struct {
	area  string
	level auth.Level
}

// required returns what a request needs. r.Pattern is the matched ServeMux pattern.
func required(r *http.Request) need {
	if n, ok := special[r.Pattern]; ok {
		return n
	}
	path := strings.TrimPrefix(r.Pattern[strings.IndexByte(r.Pattern, ' ')+1:], "/v1/")
	first, _, _ := strings.Cut(path, "/")
	area, ok := areaByPrefix[first]
	if !ok {
		return need{auth.AreaSettings, auth.Edit}
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return need{area, auth.View}
	}
	return need{area, auth.Edit}
}

func areaName(a string) string {
	if a == "" {
		return "this"
	}
	return a
}
