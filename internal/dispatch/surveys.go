package dispatch

// Survey links ({{survey.ID}}, package survey): each message gets its contact's own page link,
// carrying the message's sealed tracking token so the answer is tied to them and the campaign.

import (
	"encoding/json"
	"fmt"
	"strconv"

	"pulse/internal/channel"
	"pulse/internal/model"
	"pulse/internal/personalize"
	"pulse/internal/tracking"
)

// surveyIDs are the surveys a template links to (cached per template version).
func (d *Dispatcher) surveyIDs(t model.Template) []string {
	key := fmt.Sprintf("s%d:%d", t.ID, t.UpdatedAt.UnixNano())
	if c, ok := d.recoNeeds.Load(key); ok {
		return c.([]string)
	}
	raw, _ := json.Marshal(t)
	ids := personalize.SurveyIDs(string(raw))
	storeBounded(&d.recoNeeds, &d.recoCached, maxRecoNeeds, key, ids)
	return ids
}

// withSurveys fills the survey links; token is the email's tracking token ("" on other channels,
// where one is made for the links).
func (d *Dispatcher) withSurveys(t model.Template, m model.Message, c model.Contact, token string, v *channel.Vars) {
	ids := d.surveyIDs(t)
	if len(ids) == 0 || d.cfg.Tracking == nil {
		return
	}
	if token == "" {
		token = d.cfg.Tracking.Encode(tracking.Token{TenantID: m.TenantID, ExternalID: c.ExternalID, MessageID: m.ID,
			Channel: m.Channel, TemplateID: m.TemplateID, JourneyID: m.JourneyID, BroadcastID: m.BroadcastID, Run: m.Run, Variant: m.Variant})
	}
	v.Surveys = make(map[string]string, len(ids))
	for _, id := range ids {
		n, _ := strconv.ParseInt(id, 10, 64)
		v.Surveys[id] = d.cfg.Tracking.SurveyURL(n, token)
	}
}
