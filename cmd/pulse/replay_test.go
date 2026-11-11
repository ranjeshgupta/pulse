package main

import "testing"

func TestReplayTarget(t *testing.T) {
	ok := map[string]string{
		"email":               "messages.email",
		"messages.sms":        "messages.sms",
		"messages.push.dlq":   "messages.push",
		"delivery.receipts":   "delivery.receipts",
		"events.raw.dlq":      "events.raw",
		" segment.membership": "segment.membership",
	}
	for in, want := range ok {
		if got, err := replayTarget(in); err != nil || got != want {
			t.Errorf("replayTarget(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "fax", "messages.fax", ".dlq"} {
		if got, err := replayTarget(bad); err == nil {
			t.Errorf("replayTarget(%q) = %q, want an error", bad, got)
		}
	}
}

// Every pipeline topic, delivery receipts included, is created with its DLQ by migrate.
func TestPipelineTopicsIncludeReceipts(t *testing.T) {
	found := false
	for _, tp := range pipelineTopics() {
		found = found || tp == "delivery.receipts"
	}
	if !found {
		t.Error("delivery.receipts missing from the topics migrate creates")
	}
}
