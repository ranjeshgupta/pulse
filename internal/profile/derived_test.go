package profile

import (
	"reflect"
	"testing"
	"time"

	"pulse/internal/consent"
	"pulse/internal/event"
)

func TestDeviceLocaleAndEmailDomain(t *testing.T) {
	cur := map[string]any{"device_model": "SM-S918B", "device_brand": "Samsung", "email": "a@old.com", "email_domain": "old.com"}
	e := &event.Event{Name: event.PageView, Email: "Asha@Example.COM", Props: map[string]any{
		"device_type": "desktop", "os": "Windows", "os_version": "11", "browser": "Chrome", "locale": "en-IN", "url": "x",
	}}
	got, changed := Merge(cur, e)
	want := map[string]any{"device_type": "desktop", "device_os": "Windows", "device_os_version": "11", "browser": "Chrome",
		"locale": "en-IN", "email": "Asha@Example.COM", "email_domain": "example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged = %v\nwant %v", got, want)
	}
	for _, k := range []string{"device_model", "device_brand", "device_type", "email_domain"} {
		if !contains(changed, k) {
			t.Errorf("changed %v misses %s", changed, k)
		}
	}
	// Custom events don't touch the device, and attributes can't set it.
	got, _ = Merge(want, &event.Event{Name: "purchase", Props: map[string]any{"os": "Linux"}, Attrs: map[string]any{"device_os": "Hack", "email_domain": "x.y"}})
	if got["device_os"] != "Windows" || got["email_domain"] != "example.com" {
		t.Errorf("custom event changed derived attributes: %v", got)
	}
	// A server's own locale wins over the SDK's on the same event.
	got, _ = Merge(nil, &event.Event{Name: event.Identify, Attrs: map[string]any{"locale": "hi-IN"}, Props: map[string]any{"locale": "en-US"}})
	if got["locale"] != "hi-IN" {
		t.Errorf("locale = %v", got["locale"])
	}
}

func TestOwnerAndSMSBounce(t *testing.T) {
	got, _ := Merge(nil, &event.Event{Name: event.OwnerChanged, Props: map[string]any{"owner_id": 7.0, "owner_name": "Ravi"}})
	if got["owner_id"] != 7.0 || got["owner_name"] != "Ravi" {
		t.Fatalf("owner = %v", got)
	}
	got, changed := Merge(got, &event.Event{Name: event.OwnerChanged, Props: map[string]any{"owner_id": 0.0}})
	if len(got) != 0 || !contains(changed, "owner_id") {
		t.Errorf("unassign left %v (changed %v)", got, changed)
	}
	if got, _ := Merge(nil, &event.Event{Name: "identify", Attrs: map[string]any{"owner_id": 3}}); got["owner_id"] != nil {
		t.Error("owner_id can't be set by attributes")
	}

	bounced := map[string]any{"phone": "+919800000001", "sms_bounced": true}
	if got, _ := Merge(bounced, &event.Event{Name: "identify", Attrs: map[string]any{"phone": "+919800000001"}}); got["sms_bounced"] != true {
		t.Error("the same number stays bounced")
	}
	if got, _ := Merge(bounced, &event.Event{Name: "identify", Attrs: map[string]any{"phone": "+919800000002"}}); got["sms_bounced"] != false {
		t.Error("a new number clears sms_bounced")
	}
	if !MergeIdentity(map[string]any{"sms_bounced": true}, map[string]any{})["sms_bounced"].(bool) {
		t.Error("sms_bounced is sticky on merge")
	}
}

func TestNormalizeConsent(t *testing.T) {
	for src, want := range map[string]string{"email_link": "self", "one_click": "self", "sms_keyword": "self", "preference_center": "self",
		"form": "self", "complaint": "self", "pulse_app": "manual", "": "manual", "import": "manual"} {
		e := &event.Event{Name: consent.Unsubscribed, Props: map[string]any{"channel": "email", "source": src}}
		NormalizeConsent(e)
		if e.Props["method"] != want {
			t.Errorf("source %q: method %v, want %s", src, e.Props["method"], want)
		}
	}
	e := &event.Event{Name: consent.Unsubscribed}
	NormalizeConsent(e)
	if e.Props["method"] != "manual" {
		t.Errorf("no props: %v", e.Props)
	}
	e = &event.Event{Name: "purchase", Props: map[string]any{}}
	if NormalizeConsent(e); len(e.Props) != 0 {
		t.Error("other events are left alone")
	}
}

func TestStampDates(t *testing.T) {
	ts := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	created := ts.Add(-time.Hour)
	p, changed := StampDates(map[string]any{"city": "Pune"}, []string{"city"}, ts, created, "anon-1")
	if p["date_added"] != created.Format(time.RFC3339) || p["date_modified"] != "2026-11-10" || p["date_identified"] != nil {
		t.Errorf("anonymous: %v", p)
	}
	if !contains(changed, "date_added") || !contains(changed, "date_modified") {
		t.Errorf("changed %v", changed)
	}
	// Same day: nothing more to write. An email identifies an anonymous contact.
	p2, changed := StampDates(p, []string{"city"}, ts.Add(time.Hour), created, "anon-1")
	if len(changed) != 1 || p2["date_modified"] != "2026-11-10" {
		t.Errorf("same day rewrote: %v", changed)
	}
	p["email"] = "a@b.co"
	p3, _ := StampDates(p, nil, ts.Add(2*time.Hour), created, "anon-1")
	if p3["date_identified"] != ts.Add(2*time.Hour).Format(time.RFC3339) {
		t.Errorf("identified = %v", p3["date_identified"])
	}
	// Only seen-stamps changed: date_modified stays.
	p4, _ := StampDates(map[string]any{"date_added": "x", "date_identified": "y"}, []string{"last_seen"}, ts, created, "u1")
	if p4["date_modified"] != nil {
		t.Errorf("last_seen alone moved date_modified: %v", p4)
	}
}

func TestDerivedAttributesAreReserved(t *testing.T) {
	for _, k := range []string{"date_added", "date_identified", "date_modified", "email_domain", "owner_id", "owner_name", "sms_bounced",
		"device_type", "device_os", "device_os_version", "device_brand", "device_model", "browser", "app_version"} {
		if !consent.IsReserved(k) {
			t.Errorf("%s should be reserved", k)
		}
	}
	for _, a := range event.DeviceProps {
		if !event.EventOnlyAttr(a) {
			t.Errorf("%s should be event-only", a)
		}
	}
	if event.EventOnlyAttr("locale") || event.EventOnlyAttr("sms_bounced") || consent.IsReserved("locale") {
		t.Error("locale is settable; sms_bounced can be cleared by a server")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
