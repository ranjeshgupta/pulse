package providers

import (
	"net/url"
	"testing"
)

func TestTwilioInbound(t *testing.T) {
	p := &Twilio{AuthToken: "tok"}
	const public = "https://pulse.example.com/v1/webhooks/providers/7?t=1"
	form := url.Values{"MessageSid": {"SM9"}, "SmsStatus": {"received"}, "From": {"+919812345678"}, "Body": {"Stop"}, "To": {"+14155550123"}}
	got, err := p.ParseReceipts(twilioCallback(form, twilioSign("tok", public, form)))
	if err != nil {
		t.Fatal(err)
	}
	want := Receipt{ProviderMessageID: "SM9", Status: Inbound, From: "+919812345678", Text: "Stop"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if _, err := p.ParseReceipts(twilioCallback(form, twilioSign("other", public, form))); err == nil {
		t.Error("an unsigned inbound message must be refused")
	}
}

func TestMetaWhatsAppInbound(t *testing.T) {
	body := `{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{
 "messaging_product":"whatsapp","messages":[
  {"from":"919812345678","id":"wamid.IN1","timestamp":"1","type":"text","text":{"body":"STOP"}},
  {"from":"919812345679","id":"wamid.IN2","timestamp":"2","type":"button","button":{"text":"Stop promotions","payload":"x"}}]}}]}]}`
	got, err := (&MetaWhatsApp{AppSecret: "s3cret"}).ParseReceipts(waWebhook(body, "s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Receipt{
		{ProviderMessageID: "wamid.IN1", Status: Inbound, From: "+919812345678", Text: "STOP"},
		{ProviderMessageID: "wamid.IN2", Status: Inbound, From: "+919812345679", Text: "Stop promotions"},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
