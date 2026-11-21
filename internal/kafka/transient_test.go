package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

type sqlErr string

func (e sqlErr) Error() string    { return "pg error " + string(e) }
func (e sqlErr) SQLState() string { return string(e) }

func TestTransient(t *testing.T) {
	var syntax *json.SyntaxError
	_ = json.Unmarshal([]byte("{"), &syntax)
	cases := map[string]struct {
		err  error
		want bool
	}{
		"redis loading":      {errors.New("LOADING Redis is loading the dataset in memory"), true},
		"refused":            {fmt.Errorf("dial: %w", syscall.ECONNREFUSED), true},
		"net timeout":        {&net.OpError{Op: "read", Err: errors.New("i/o timeout")}, true},
		"deadline":           {context.DeadlineExceeded, true},
		"pg connection":      {sqlErr("08006"), true},
		"pg shutdown":        {sqlErr("57P01"), true},
		"pg bad input":       {sqlErr("22P02"), false},
		"bad json":           {json.Unmarshal([]byte("{"), &struct{}{}), false},
		"validation":         {errors.New("event name is required"), false},
		"cancelled shutdown": {context.Canceled, false},
		"nil":                {nil, false},
	}
	for name, c := range cases {
		if got := Transient(c.err); got != c.want {
			t.Errorf("%s: Transient(%v) = %v, want %v", name, c.err, got, c.want)
		}
	}
}
