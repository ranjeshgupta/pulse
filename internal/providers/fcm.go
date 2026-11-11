package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"

	"pulse/internal/channel"
)

func init() {
	register(Kind{
		ID: "fcm", Channel: channel.Push, Name: "Firebase Cloud Messaging",
		Description: "Push notifications to Android, iOS and web apps through FCM (HTTP v1 API).",
		Config:      []Field{{Key: "project_id", Label: "Firebase project ID", Placeholder: "acme-app-1a2b3", Required: true}},
		Secrets: []Field{{Key: "service_account_json", Label: "Service account key (JSON) from the Firebase console",
			Placeholder: `{"type": "service_account", ...}`, Required: true}},
		New: func(d Deps, c, s map[string]string) (Provider, error) {
			ts, err := serviceAccountTokens(d, s["service_account_json"], fcmScope)
			if err != nil {
				return nil, fmt.Errorf("fcm: %w", err)
			}
			return &FCM{HTTP: d.HTTP, ProjectID: c["project_id"], baseURL: "https://fcm.googleapis.com", tokens: ts}, nil
		},
	})
}

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// FCM sends one notification per device token through the HTTP v1 API. Dead tokens come back as
// ErrUnreachable so the dispatcher deletes them.
type FCM struct {
	HTTP      *http.Client
	ProjectID string
	baseURL   string
	tokens    oauth2.TokenSource
}

func (f *FCM) Send(ctx context.Context, o Outbound) (Result, error) {
	c := o.Rendered.Content
	notif := map[string]string{"title": c.Title, "body": c.Body}
	if c.ImageURL != "" {
		notif["image"] = c.ImageURL
	}
	msg := map[string]any{"token": o.To, "notification": notif}
	data := maps.Clone(c.Data)
	if c.URL != "" {
		if data == nil {
			data = map[string]string{}
		}
		data["url"] = c.URL
	}
	if len(data) > 0 {
		msg["data"] = data
	}
	if o.Platform == "web" && strings.HasPrefix(c.URL, "https://") { // FCM rejects non-https links
		msg["webpush"] = map[string]any{"fcm_options": map[string]string{"link": c.URL}}
	}
	req, err := jsonRequest(ctx, f.baseURL+"/v1/projects/"+url.PathEscape(f.ProjectID)+"/messages:send",
		map[string]any{"message": msg})
	if err != nil {
		return Result{}, err
	}
	tok, err := bearer("fcm", f.tokens)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	code, body, hdr, err := call(f.HTTP, req)
	if err != nil {
		return Result{}, err
	}
	if code >= 200 && code < 300 {
		var r struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(body, &r)
		return Result{ProviderMessageID: r.Name}, nil
	}
	var e struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	fcmCode := e.Error.Status
	for _, d := range e.Error.Details {
		if d.ErrorCode != "" {
			fcmCode = d.ErrorCode
		}
	}
	reason := fmt.Sprintf("fcm: HTTP %d %s: %s", code, fcmCode, e.Error.Message)
	switch {
	case code == http.StatusNotFound, fcmCode == "UNREGISTERED",
		// The token belongs to another Firebase project: it will never work with this one.
		fcmCode == "SENDER_ID_MISMATCH",
		code == http.StatusBadRequest && fcmCode == "INVALID_ARGUMENT" && strings.Contains(strings.ToLower(e.Error.Message), "token"):
		return Result{}, Unreachable(reason)
	}
	return Result{}, StatusError("fcm", code, string(body), hdr)
}
