package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"golang.org/x/oauth2"

	"pulse/internal/channel"
)

func init() {
	register(Kind{
		ID: "google_rbm", Channel: channel.RCS, Name: "Google RCS Business Messaging",
		Description: "Rich RCS messages (cards, buttons) through Google's RBM API, from a launched agent.",
		Config:      []Field{{Key: "agent_id", Label: "Agent ID", Placeholder: "acme_agent_abc123", Required: true}},
		Secrets: []Field{{Key: "service_account_json", Label: "Service account key (JSON) with RBM access",
			Placeholder: `{"type": "service_account", ...}`, Required: true}},
		New: func(d Deps, c, s map[string]string) (Provider, error) {
			ts, err := serviceAccountTokens(d, s["service_account_json"], rbmScope)
			if err != nil {
				return nil, fmt.Errorf("google rbm: %w", err)
			}
			return &GoogleRBM{HTTP: d.HTTP, AgentID: c["agent_id"], baseURL: "https://rcsbusinessmessaging.googleapis.com",
				tokens: ts}, nil
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
