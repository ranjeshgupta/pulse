package providers

import "testing"

func TestCheckWebPushSubscription(t *testing.T) {
	ok := []string{
		`{"endpoint":"https://fcm.googleapis.com/fcm/send/abc","keys":{}}`,
		`{"endpoint":"https://updates.push.services.mozilla.com/wpush/v2/x"}`,
		`{"endpoint":"https://web.push.apple.com/QX"}`,
		`{"endpoint":"https://db5p.notify.windows.com/w/?token=1"}`,
	}
	for _, s := range ok {
		if err := CheckWebPushSubscription(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	bad := []string{
		`{"endpoint":"http://fcm.googleapis.com/fcm/send/abc"}`,
		`{"endpoint":"https://169.254.169.254/latest/meta-data"}`,
		`{"endpoint":"https://internal.local/hook"}`,
		`{"endpoint":"https://fcm.googleapis.com.evil.test/x"}`,
		`{"endpoint":"https://evilfcm.googleapis.com@10.0.0.1/x"}`,
		`{"endpoint":"https://fcm.googleapis.com:8443/x"}`,
		`{"endpoint":""}`, `{`,
	}
	for _, s := range bad {
		if CheckWebPushSubscription(s) == nil {
			t.Errorf("accepted %s", s)
		}
	}
}
