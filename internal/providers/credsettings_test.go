package providers

import "testing"

func TestCredentialSettingsChanged(t *testing.T) {
	base := map[string]string{"url": "https://sms.example.com/send", "mode": "custom", "from": "ACME", "auth": "{}"}
	same := map[string]string{"url": "https://sms.example.com/send", "mode": "custom", "from": "SHOP", "auth": "{}"}
	if CredentialSettingsChanged(base, same) {
		t.Error("changing the sender counted")
	}
	for k, v := range map[string]string{"url": "https://sms.example.com/other?x=1", "mode": "standard", "auth": `{"type":"basic"}`, "receipt": "x", "username": "u"} {
		after := map[string]string{}
		for a, b := range base {
			after[a] = b
		}
		after[k] = v
		if !CredentialSettingsChanged(base, after) {
			t.Errorf("changing %s not counted", k)
		}
	}
	gone := map[string]string{"url": "https://sms.example.com/send", "from": "ACME", "auth": "{}"}
	if !CredentialSettingsChanged(base, gone) {
		t.Error("removing mode not counted")
	}
}
