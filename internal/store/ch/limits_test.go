package ch

import (
	"context"
	"testing"
	"time"
)

func TestWithLimits(t *testing.T) {
	set := Limits{MaxExecutionTime: 90 * time.Second, MaxMemory: 1 << 30}.settings()
	if set["max_execution_time"] != 90 || set["max_memory_usage"] != int64(1<<30) || set["timeout_overflow_mode"] != "throw" {
		t.Errorf("settings = %v", set)
	}
	if got := WithLimits(context.Background(), Limits{}); got != context.Background() {
		t.Error("no limits should leave the context alone")
	}
	if WithLimits(context.Background(), DefaultLimits) == context.Background() {
		t.Error("limits not applied")
	}
}
