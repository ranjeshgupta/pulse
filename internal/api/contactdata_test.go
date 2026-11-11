package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"pulse/internal/model"
)

type memDeleter struct{ deleted map[int64][]uint32 }

func (d *memDeleter) DeleteContacts(_ context.Context, tenant int64, ids []uint32) error {
	d.deleted[tenant] = append(d.deleted[tenant], ids...)
	return nil
}

func TestDeleteContact(t *testing.T) {
	s, _, _ := newServer()
	del := &memDeleter{deleted: map[int64][]uint32{}}
	s.Deleter = del
	h := s.Handler()
	if w := do(t, h, "DELETE", "/v1/contacts/u1", "key-2", ""); w.Code != 404 {
		t.Errorf("another workspace's contact: %d", w.Code)
	}
	if w := do(t, h, "DELETE", "/v1/contacts/nobody", "key-1", ""); w.Code != 404 {
		t.Errorf("unknown contact: %d", w.Code)
	}
	if w := do(t, h, "DELETE", "/v1/contacts/u1", "key-1", ""); w.Code != 200 || len(del.deleted[1]) != 1 || del.deleted[1][0] != 1 {
		t.Fatalf("delete: %d %v", w.Code, del.deleted)
	}
	if len(del.deleted[2]) != 0 {
		t.Fatal("deleted in the wrong workspace")
	}
}

func TestExportContactData(t *testing.T) {
	s, store, _ := newServer()
	store.segments = []model.Segment{{ID: 1, TenantID: 1, Name: "VIP", Kind: "rule"}, {ID: 2, TenantID: 1, Name: "Other"}}
	store.contacts[1]["u/1"] = model.Contact{TenantID: 1, ID: 7, ExternalID: "u/1", Email: "a@example.com",
		Attrs: map[string]any{"first_name": "Asha", "optout_sms": true}}
	h := s.Handler()
	w := do(t, h, "GET", "/v1/contacts/u%2F1/data", "key-1", "")
	if w.Code != 200 {
		t.Fatalf("export: %d %s", w.Code, w.Body)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="contact-u_1.json"`) {
		t.Errorf("download name: %s", cd)
	}
	var out struct {
		Contact map[string]any `json:"contact"`
		Consent struct {
			Channels []struct {
				Channel    string
				Subscribed bool
			}
		} `json:"consent"`
		Segments []struct{ Name string } `json:"segments"`
		Events   []any                   `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Contact["email"] != "a@example.com" || len(out.Segments) != 1 || out.Segments[0].Name != "VIP" || out.Events == nil {
		t.Errorf("export: %s", w.Body)
	}
	for _, c := range out.Consent.Channels {
		if want := c.Channel != "sms"; c.Subscribed != want {
			t.Errorf("consent %s = %v", c.Channel, c.Subscribed)
		}
	}
	if w := do(t, h, "GET", "/v1/contacts/u%2F1/data", "key-2", ""); w.Code != 404 {
		t.Errorf("another workspace: %d", w.Code)
	}
}
