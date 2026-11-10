package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"pulse/internal/auth"
	"pulse/internal/journey"
	"pulse/internal/model"
)

func TestDeleteContactStepNeedsContactsEdit(t *testing.T) {
	g, err := journey.ParseGraph([]byte(`{"trigger":{"type":"event","event":"gdpr_erase"},"entry":"d","nodes":{"d":{"type":"delete_contact","confirm":"DELETE"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	marketer := model.Principal{TenantID: 1, Perms: auth.Perms{auth.AreaJourneys: auth.Publish, auth.AreaContacts: auth.View}}
	w := httptest.NewRecorder()
	if deletePermOK(w, marketer, g) || w.Code != 403 || !strings.Contains(w.Body.String(), "edit access to contacts") {
		t.Errorf("a journey editor without contacts edit saved a delete step: %d %s", w.Code, w.Body)
	}
	editor := model.Principal{TenantID: 1, Perms: auth.Perms{auth.AreaJourneys: auth.Publish, auth.AreaContacts: auth.Edit}}
	if !deletePermOK(httptest.NewRecorder(), editor, g) {
		t.Error("contacts editors may")
	}
	plain, _ := journey.ParseGraph([]byte(`{"trigger":{"type":"event","event":"x"},"entry":"e","nodes":{"e":{"type":"exit"}}}`))
	if !deletePermOK(httptest.NewRecorder(), marketer, plain) {
		t.Error("journeys without a delete step need nothing more")
	}
}

func TestConsentAndCRMStepsNeedContactsEdit(t *testing.T) {
	marketer := model.Principal{TenantID: 1, Perms: auth.Perms{auth.AreaJourneys: auth.Publish, auth.AreaContacts: auth.View}}
	for _, step := range []string{`{"type":"dnc_remove","channel":"email","next":"e"}`, `{"type":"update_owner","owner_id":0,"next":"e"}`,
		`{"type":"add_company","company_id":3,"next":"e"}`} {
		g, err := journey.ParseGraph([]byte(`{"trigger":{"type":"event","event":"x"},"entry":"s","nodes":{"s":` + step + `,"e":{"type":"exit"}}}`))
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		w := httptest.NewRecorder()
		if deletePermOK(w, marketer, g) || w.Code != 403 || !strings.Contains(w.Body.String(), "edit access to contacts") {
			t.Errorf("%s saved without contacts edit: %d %s", step, w.Code, w.Body)
		}
	}
	add, _ := journey.ParseGraph([]byte(`{"trigger":{"type":"event","event":"x"},"entry":"s","nodes":{"s":{"type":"dnc_add","channel":"email","next":"e"},"e":{"type":"exit"}}}`))
	if !deletePermOK(httptest.NewRecorder(), marketer, add) {
		t.Error("adding Do Not Contact only stops messages and needs nothing more")
	}
}

func TestJourneyRefsRefuseItself(t *testing.T) {
	s := New(Deps{Store: &memStore{}})
	g, err := journey.ParseGraph([]byte(`{"trigger":{"type":"event","event":"x"},"entry":"c","nodes":{
		"c":{"type":"change_journey","add_journeys":[7],"next":"e"},"e":{"type":"exit"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.checkJourneyRefs(context.Background(), 1, 7, g); !errors.Is(err, errBadJourney) || !strings.Contains(err.Error(), "not this one") {
		t.Errorf("err %v", err)
	}
}
