package providers

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"

	"pulse/internal/channel"
)

func init() {
	register(Kind{
		ID: "google_rbm", Channel: channel.RCS, Name: "Google RCS Business Messaging",
		Description: "Rich RCS messages (cards, buttons) through Google's RBM API, from a launched agent.",
		Config:      []Field{{Key: "agent_id", Label: "Agent ID", Placeholder: "acme_agent_abc123", Required: true}},
		Secrets: []Field{{Key: "service_account_json", Label: "Service account key (JSON) with RBM access",
			Placeholder: `{"type": "service_account", ...}`, Required: true},
			{Key: "webhook_client_token", Label: "Webhook client token (for replies, delivery and read receipts)",
				Placeholder: "the client token you set on the agent's webhook in the RBM console"}},
		New: func(d Deps, c, s map[string]string) (Provider, error) {
			ts, err := serviceAccountTokens(d, s["service_account_json"], rbmScope)
			if err != nil {
				return nil, fmt.Errorf("google rbm: %w", err)
			}
			return &GoogleRBM{HTTP: d.HTTP, AgentID: c["agent_id"], baseURL: "https://rcsbusinessmessaging.googleapis.com",
				tokens: ts, ClientToken: s["webhook_client_token"]}, nil
		},
	})
}

const rbmScope = "https://www.googleapis.com/auth/rcsbusinessmessaging"

// GoogleRBM sends agent messages through the RCS Business Messaging API. Pulse's message id is the
// RBM messageId, so a retried send cannot deliver twice (RBM answers 409) and RBM's own events refer
// to it.
type GoogleRBM struct {
	HTTP    *http.Client
	AgentID string
	baseURL string
	tokens  oauth2.TokenSource
	// ClientToken is the webhook's client token: it answers the URL check and signs every message.
	ClientToken string
}

func rbmSuggestion(s channel.Suggestion) map[string]any {
	postback := s.Value
	if postback == "" || s.Type != "reply" {
		postback = s.Type + ":" + s.Text
	}
	switch s.Type {
	case "url":
		return map[string]any{"action": map[string]any{"text": s.Text, "postbackData": postback,
			"openUrlAction": map[string]string{"url": s.Value}}}
	case "dial":
		return map[string]any{"action": map[string]any{"text": s.Text, "postbackData": postback,
			"dialAction": map[string]string{"phoneNumber": s.Value}}}
	default:
		return map[string]any{"reply": map[string]string{"text": s.Text, "postbackData": postback}}
	}
}

func (g *GoogleRBM) Send(ctx context.Context, o Outbound) (Result, error) {
	c := o.Rendered.Content
	var sugg []map[string]any
	for _, s := range c.Suggestions {
		sugg = append(sugg, rbmSuggestion(s))
	}
	msg := map[string]any{}
	if c.Card != nil {
		content := map[string]any{"title": c.Card.Title}
		if c.Card.Description != "" {
			content["description"] = c.Card.Description
		}
		if c.Card.ImageURL != "" {
			content["media"] = map[string]any{"height": "MEDIUM", "contentInfo": map[string]string{"fileUrl": c.Card.ImageURL}}
		}
		if len(sugg) > 0 {
			content["suggestions"] = sugg
		}
		// A content message carries text or a card, not both: with a card, Text is not sent.
		msg["richCard"] = map[string]any{"standaloneCard": map[string]any{"cardOrientation": "VERTICAL", "cardContent": content}}
	} else {
		msg["text"] = c.Text
		if len(sugg) > 0 {
			msg["suggestions"] = sugg
		}
	}
	q := url.Values{"messageId": {o.MessageID}, "agentId": {g.AgentID}}
	req, err := jsonRequest(ctx, g.baseURL+"/v1/phones/"+url.PathEscape(o.To)+"/agentMessages?"+q.Encode(),
		map[string]any{"contentMessage": msg})
	if err != nil {
		return Result{}, err
	}
	tok, err := bearer("google rbm", g.tokens)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	code, body, err := call(g.HTTP, req)
	if err != nil {
		return Result{}, err
	}
	switch {
	case code >= 200 && code < 300, code == http.StatusConflict: // 409: this messageId was already sent
		return Result{ProviderMessageID: o.MessageID}, nil
	case code == http.StatusNotFound: // the phone isn't RCS-capable (or not reachable by this agent)
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return Result{}, Unreachable("google rbm: " + e.Error.Message)
	}
	return Result{}, StatusError("google rbm", code, string(body))
}

// BodyChallenge answers RBM's webhook check: a POST {clientToken, secret} that must be echoed as
// {secret} when the client token is the agent's.
func (g *GoogleRBM) BodyChallenge(body []byte) ([]byte, bool, bool) {
	var c struct {
		ClientToken string `json:"clientToken"`
		Secret      string `json:"secret"`
	}
	if json.Unmarshal(body, &c) != nil || c.Secret == "" || c.ClientToken == "" {
		return nil, false, false
	}
	if g.ClientToken == "" || !equalSecret(c.ClientToken, g.ClientToken) {
		return nil, true, false
	}
	out, _ := json.Marshal(map[string]string{"secret": c.Secret})
	return out, true, true
}

// rbmUserMessage is the decoded data of an RBM webhook message: a user message (text or a
// tapped suggestion) or an event about an agent message (delivered, read).
type rbmUserMessage struct {
	SenderPhoneNumber  string `json:"senderPhoneNumber"`
	MessageID          string `json:"messageId"`
	EventType          string `json:"eventType"`
	Text               string `json:"text"`
	SuggestionResponse *struct {
		PostbackData string `json:"postbackData"`
		Text         string `json:"text"`
		Type         string `json:"type"` // REPLY | ACTION
	} `json:"suggestionResponse"`
}

// ParseReceipts reads an RBM webhook delivery: {"message": {"data": base64 JSON, ...}}. The
// X-Goog-Signature header must be the base64 HMAC-SHA512 of the decoded data, keyed by the client
// token. DELIVERED and READ events become receipts for Pulse's message id; text and tapped
// suggestions become inbound messages. Typing indicators and files are ignored.
func (g *GoogleRBM) ParseReceipts(r *http.Request) ([]Receipt, error) {
	body, err := webhookBody(r)
	if err != nil {
		return nil, err
	}
	var env struct {
		Message struct {
			Data string `json:"data"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("google rbm webhook: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(env.Message.Data)
	if err != nil {
		return nil, fmt.Errorf("google rbm webhook: message data is not base64")
	}
	if g.ClientToken == "" {
		return nil, ErrUnauthorized
	}
	mac := hmac.New(sha512.New, []byte(g.ClientToken))
	mac.Write(data)
	if !equalSecret(r.Header.Get("X-Goog-Signature"), base64.StdEncoding.EncodeToString(mac.Sum(nil))) {
		return nil, ErrUnauthorized
	}
	return parseRBMMessage(data)
}

func parseRBMMessage(data []byte) ([]Receipt, error) {
	var m rbmUserMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("google rbm webhook: %w", err)
	}
	switch strings.ToUpper(m.EventType) {
	case "DELIVERED":
		return []Receipt{{ProviderMessageID: m.MessageID, Status: "delivered"}}, nil
	case "READ":
		return []Receipt{{ProviderMessageID: m.MessageID, Status: "read"}}, nil
	case "":
	default: // IS_TYPING and anything new
		return nil, nil
	}
	from := strings.ReplaceAll(m.SenderPhoneNumber, " ", "")
	if from == "" {
		return nil, nil
	}
	rc := Receipt{ProviderMessageID: m.MessageID, Status: Inbound, From: from, Text: m.Text}
	if sr := m.SuggestionResponse; sr != nil {
		rc.Text, rc.Postback = sr.Text, sr.PostbackData
		rc.Suggestion = "reply"
		if strings.EqualFold(sr.Type, "ACTION") {
			rc.Suggestion = "action"
		}
	}
	if rc.Text == "" && rc.Suggestion == "" {
		return nil, nil // a file or location share: nothing Pulse reads
	}
	return []Receipt{rc}, nil
}
