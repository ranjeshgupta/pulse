package providers

import (
	"context"
	"encoding/json"
	"strings"

	"pulse/internal/channel"
)

// SandboxSink stores sandbox messages for the app's test inbox.
type SandboxSink interface {
	RecordSandbox(ctx context.Context, tenant int64, ch, recipient, messageID string, payload []byte) error
}

// The sandbox provider delivers nothing: it records every message so it can be inspected in the
// app's test inbox. Recipients ending with the "unreachable suffix" behave as if the number is not on
// WhatsApp / RCS (or the push token is dead), so fallbacks can be tested without real accounts.
func init() {
	for _, ch := range channel.External {
		register(Kind{
			ID: "sandbox_" + ch, Channel: ch, Name: "Sandbox (test inbox)",
			Description: "Records messages in Pulse's test inbox instead of delivering them.",
			Config: []Field{{Key: "unreachable_suffix", Label: "Treat recipients ending with this as unreachable",
				Placeholder: "0000"}},
			New: func(d Deps, config, _ map[string]string) (Provider, error) {
				return &Sandbox{Sink: d.Sandbox, Channel: ch, UnreachableSuffix: config["unreachable_suffix"]}, nil
			},
		})
	}
}

type Sandbox struct {
	Sink              SandboxSink
	Channel           string
	UnreachableSuffix string
}

func (s *Sandbox) Send(ctx context.Context, o Outbound) (Result, error) {
	if s.UnreachableSuffix != "" && strings.HasSuffix(o.To, s.UnreachableSuffix) {
		return Result{}, Unreachable("sandbox: " + o.To + " is configured as unreachable")
	}
	payload, err := json.Marshal(map[string]any{
		"subject": o.Rendered.Subject, "html": o.Rendered.HTML, "content": o.Rendered.Content, "platform": o.Platform,
	})
	if err != nil {
		return Result{}, err
	}
	if err := s.Sink.RecordSandbox(ctx, o.TenantID, s.Channel, o.To, o.MessageID, payload); err != nil {
		return Result{}, err
	}
	return Result{ProviderMessageID: "sandbox-" + o.MessageID}, nil
}
