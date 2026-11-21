package ch

import (
	"context"
	"errors"
	"fmt"
	"github.com/ClickHouse/clickhouse-go/v2"
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

func TestSubSecondTimeoutIsStillALimit(t *testing.T) {
	set := Limits{MaxExecutionTime: 500 * time.Millisecond}.settings()
	if set["max_execution_time"] != 1 {
		t.Errorf("500ms became %v, want 1 (0 would mean no limit)", set["max_execution_time"])
	}
	if set := (Limits{MaxExecutionTime: 1500 * time.Millisecond}).settings(); set["max_execution_time"] != 2 {
		t.Errorf("1.5s became %v, want 2", set["max_execution_time"])
	}
}

func TestLimitExceeded(t *testing.T) {
	for code, want := range map[int32]bool{159: true, 160: true, 241: true, 36: false, 62: false} {
		err := fmt.Errorf("query: %w", &clickhouse.Exception{Code: code, Message: "x"})
		if LimitExceeded(err) != want {
			t.Errorf("code %d: %v", code, !want)
		}
	}
	if LimitExceeded(errors.New("plain")) || LimitExceeded(nil) {
		t.Error("non-ClickHouse error")
	}
}
