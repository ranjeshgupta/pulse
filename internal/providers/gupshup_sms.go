package providers

import (
	"context"
	"net/http"

	"pulse/internal/channel"
)

func init() {
	register(Kind{
		ID: "gupshup_sms", Channel: channel.SMS, Name: "Gupshup SMS (Enterprise)",
		Description: "DLT-compliant SMS in India through Gupshup's Enterprise HTTP API, with delivery reports.",
		Notes: "Endpoints and parameters are built from Gupshup's public docs (Enterprise GatewayAPI SendMessage, " +
			"real-time delivery reports) and tested against mocks only: confirm them with a real account before " +
			"production use. Every message needs a DLT-registered sender (mask) and template: set the template id on " +
			"each SMS template (or a default here) and the principal entity id here. Gupshup sets throughput per " +
			"account; set this provider's rate to what your account manager confirms. Set the Realtime Delivery URL " +
			"(Settings → Advanced Account Settings) to this provider's webhook URL with ?token=…, as a POST.",
		Config: []Field{
			{Key: "base_url", Label: "API URL", Default: gupshupEnterpriseURL, Placeholder: gupshupEnterpriseURL,
				Hint: "The Enterprise GatewayAPI endpoint; change it only if Gupshup gives you another."},
			{Key: "mask", Label: "Sender ID (DLT header)", Placeholder: "ACMEIN",
				Hint: "The 6-letter header registered on DLT and with Gupshup; a template's own sender id wins."},
			{Key: "principal_entity_id", Label: "DLT principal entity ID", Placeholder: "1201159143350000000",
				Hint: "Your entity id on the DLT portal; a template's own entity id wins."},
			{Key: "dlt_template_id", Label: "Default DLT template ID", Placeholder: "1207161234567890123",
				Hint: "Used only when an SMS template has no DLT template id of its own."},
		},
		Secrets: []Field{
			{Key: "userid", Label: "User ID (Enterprise account number)", Placeholder: "2000123456", Required: true},
			{Key: "password", Label: "Password", Required: true},
			gupshupTokenField,
		},
		New: func(d Deps, c, s map[string]string) (Provider, error) {
			base, err := gupshupBaseURL("gupshup sms", c["base_url"], gupshupEnterpriseURL)
			if err != nil {
				return nil, err
			}
			return &GupshupSMS{gupshupEnterprise: gupshupEnterprise{HTTP: gupshupClient(d), BaseURL: base,
				UserID: s["userid"], Password: s["password"], WebhookToken: s["webhook_token"]},
				Mask: c["mask"], PrincipalEntityID: c["principal_entity_id"], DLTTemplateID: c["dlt_template_id"]}, nil
		},
	})
}

// GupshupSMS sends SMS through Gupshup's Enterprise gateway (method=SendMessage) and reads its
// real-time delivery reports.
type GupshupSMS struct {
	gupshupEnterprise
	Mask              string // default sender id; a template's SenderID wins
	PrincipalEntityID string // default DLT entity id; a template's DLTEntityID wins
	DLTTemplateID     string // default DLT template id; a template's DLTTemplateID wins
}

func (g *GupshupSMS) build(o Outbound) gupshupRequest {
	c := o.Rendered.Content
	msgType := "TEXT"
	if !isGSM7(c.Text) {
		msgType = "UNICODE_TEXT"
	}
	form := g.baseForm(o.To, c.Text, msgType)
	set := func(key, own, def string) {
		if own == "" {
			own = def
		}
		if own != "" {
			form.Set(key, own)
		}
	}
	set("mask", c.SenderID, g.Mask)
	set("principalEntityId", c.DLTEntityID, g.PrincipalEntityID)
	set("dltTemplateId", c.DLTTemplateID, g.DLTTemplateID)
	return g.request(form)
}

func (g *GupshupSMS) Send(ctx context.Context, o Outbound) (Result, error) {
	return g.send(ctx, "gupshup sms", g.build(o))
}

// PreviewRequest returns the request Send would make for o, with the password masked.
func (g *GupshupSMS) PreviewRequest(o Outbound) (method, url string, headers map[string]string, body string) {
	return g.build(o).preview()
}

// ParseReceipts reads Gupshup's real-time delivery reports: a POSTed JSON array of events (or one
// object), a form body, or GET-style query parameters (externalId, deliveredTS, status
// SUCCESS|FAILURE|UNKNOWN, cause, phoneNo, errCode). The webhook token must match.
func (g *GupshupSMS) ParseReceipts(r *http.Request) ([]Receipt, error) {
	return parseEnterpriseReceipts(r, g.WebhookToken, "gupshup sms", channel.SMS, nil)
}

// parseEnterpriseReceipts verifies the token and parses delivery reports; other (JSON object)
// callbacks go to extra, when given.
func parseEnterpriseReceipts(r *http.Request, token, name, ch string, extra func(map[string]any) []Receipt) ([]Receipt, error) {
	if err := checkGupshupToken(r, token); err != nil {
		return nil, err
	}
	body, err := webhookBody(r)
	if err != nil {
		return nil, err
	}
	objs, form, err := enterpriseCallbacks(r, body)
	if err != nil {
		return nil, err
	}
	var out []Receipt
	if form != nil {
		if rc, ok := dlrReceipt(form.Get, name, ch); ok {
			out = append(out, rc)
		}
		return out, nil
	}
	for _, m := range objs {
		if rc, ok := dlrReceipt(mapGetter(m), name, ch); ok {
			out = append(out, rc)
		} else if extra != nil && mapGetter(m)("externalId") == "" {
			out = append(out, extra(m)...)
		}
	}
	return out, nil
}
