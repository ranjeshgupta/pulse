package segment

import (
	"context"
	"errors"

	"pulse/internal/attr"
)

// Explanation is one node of a rule evaluated for one contact, for "why is this contact in this
// segment". Groups carry their children; leaves carry the rule and what the contact has.
type Explanation struct {
	Kind     string        `json:"kind"`           // all | any | none | attr | event | segment | tag
	Rule     *Rule         `json:"rule,omitempty"` // leaves only
	Match    bool          `json:"match"`
	Value    *string       `json:"value,omitempty"` // attr: the contact's value ("" when missing)
	Count    *int          `json:"count,omitempty"` // event: events counted in the window
	Sweep    bool          `json:"sweep,omitempty"` // event: older than the counters; the sweep decides
	Children []Explanation `json:"children,omitempty"`
}

// Explain evaluates every node of the rule (no short cuts, so each shows its own result) with the
// same semantics as Eval. Event rules the counters can't answer are marked Sweep and count as not
// matching here; the segment's stored membership is what the sweep decided.
func Explain(ctx context.Context, r Rule, attrs map[string]any, c Counter) (Explanation, error) {
	if kids := r.group(); kids != nil {
		e := Explanation{Kind: "all"}
		switch {
		case r.Any != nil:
			e.Kind = "any"
		case r.None != nil:
			e.Kind = "none"
		}
		matched := 0
		for _, k := range kids {
			ke, err := Explain(ctx, k, attrs, c)
			if err != nil {
				return Explanation{}, err
			}
			if ke.Match {
				matched++
			}
			e.Children = append(e.Children, ke)
		}
		switch e.Kind {
		case "all":
			e.Match = matched == len(kids)
		case "any":
			e.Match = matched > 0
		default:
			e.Match = matched == 0
		}
		return e, nil
	}
	leaf := r
	e := Explanation{Rule: &leaf}
	switch {
	case r.Attr != "":
		e.Kind = "attr"
		v := attr.String(attrs[r.Attr])
		e.Value = &v
	case r.Tag != "":
		e.Kind = "tag"
	case r.Segment != 0:
		e.Kind = "segment"
	default:
		e.Kind = "event"
		n, err := c.Count(ctx, CounterName(r.Event, r.Where), r.Window())
		if errors.Is(err, ErrNeedsSweep) {
			e.Sweep = true
			return e, nil
		}
		if err != nil {
			return Explanation{}, err
		}
		e.Count = &n
		e.Match = cmpInt(r.Op, n, *r.Count)
		return e, nil
	}
	ok, err := Eval(ctx, r, attrs, c)
	if err != nil {
		return Explanation{}, err
	}
	e.Match = ok
	return e, nil
}
