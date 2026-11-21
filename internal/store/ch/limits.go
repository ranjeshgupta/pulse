package ch

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// Limits bound the ClickHouse queries a request may run (reports, funnels, segment previews…):
// a workspace user can build a query over billions of events, and one such query must not hold
// the cluster's CPU or memory for long.
type Limits struct {
	// MaxExecutionTime stops a query after this long (ClickHouse max_execution_time); 0: none.
	MaxExecutionTime time.Duration
	// MaxMemory caps one query's memory in bytes (max_memory_usage); 0: the server's default.
	MaxMemory int64
}

// DefaultLimits are used for interactive requests unless the operator sets others
// (PULSE_CH_QUERY_TIMEOUT, PULSE_CH_QUERY_MAX_MEMORY).
var DefaultLimits = Limits{MaxExecutionTime: 60 * time.Second, MaxMemory: 4 << 30}

// WithLimits returns ctx carrying the limits as query settings for every query run with it.
func WithLimits(ctx context.Context, l Limits) context.Context {
	set := l.settings()
	if len(set) == 0 {
		return ctx
	}
	return clickhouse.Context(ctx, clickhouse.WithSettings(set))
}

func (l Limits) settings() clickhouse.Settings {
	set := clickhouse.Settings{}
	if l.MaxExecutionTime > 0 {
		set["max_execution_time"] = int(l.MaxExecutionTime.Seconds())
		set["timeout_overflow_mode"] = "throw"
	}
	if l.MaxMemory > 0 {
		set["max_memory_usage"] = l.MaxMemory
	}
	return set
}
