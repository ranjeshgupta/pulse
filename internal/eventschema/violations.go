package eventschema

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"pulse/internal/event"
	"pulse/internal/segment"
)

// Problems are counted per workspace, event and UTC day in the hash evv:{tenant}:{event}:{day}
// (fields: checked, bad, refused, and "<code>:<property>"), kept 35 days, and the latest 20
// events with problems are kept in the list evvs:{tenant}:{event} for 14 days. Counting is best
// effort: a batch the profile role retries can count twice.
const (
	dayTTL     = 35 * 24 * time.Hour
	sampleTTL  = 14 * 24 * time.Hour
	maxSamples = 20
	maxSample  = 2048 // bytes of props kept per sample
	dayLayout  = "20060102"
)

func dayKey(tenant int64, name, day string) string {
	return fmt.Sprintf("evv:%d:%s:%s", tenant, name, day)
}
func samplesKey(tenant int64, name string) string { return fmt.Sprintf("evvs:%d:%s", tenant, name) }

// Sample is one recent event with problems.
type Sample struct {
	At         time.Time   `json:"at"`
	EventID    string      `json:"event_id"`
	ExternalID string      `json:"external_id"`
	Violations []Violation `json:"violations"`
	Props      string      `json:"props"` // JSON, personal properties removed, cut at 2 KB
}

type tallyKey struct {
	tenant    int64
	name, day string
}

// Batch collects the checks of one batch of events, written with one pipeline by Flush. Checks
// count on the day they happen (Now), not the event's own time, so history sent late shows up
// with today's problems.
type Batch struct {
	Now     time.Time
	checked map[tallyKey]int64
	bad     map[tallyKey]map[string]int64
	samples map[tallyKey][]Sample
}

// Add records that an event was checked, with its problems (none when it fit).
func (b *Batch) Add(s *Schema, e *event.Event, vs []Violation) {
	if b.checked == nil {
		b.checked, b.bad, b.samples = map[tallyKey]int64{}, map[tallyKey]map[string]int64{}, map[tallyKey][]Sample{}
	}
	at := b.Now
	if at.IsZero() {
		at = e.Ts
	}
	k := tallyKey{e.TenantID, e.Name, at.UTC().Format(dayLayout)}
	b.checked[k]++
	if len(vs) == 0 {
		return
	}
	if b.bad[k] == nil {
		b.bad[k] = map[string]int64{}
	}
	b.bad[k]["bad"]++
	for _, v := range vs {
		b.bad[k][v.Code+":"+v.Prop]++
	}
	if len(b.samples[k]) < maxSamples {
		b.samples[k] = append(b.samples[k], Sample{At: at, EventID: e.ID, ExternalID: e.ExternalID, Violations: vs, Props: redact(s, e.Props)})
	}
}

// Flush writes the batch.
func (b *Batch) Flush(ctx context.Context, rdb redis.Cmdable) error {
	if len(b.checked) == 0 {
		return nil
	}
	pipe := rdb.Pipeline()
	for k, n := range b.checked {
		key := dayKey(k.tenant, k.name, k.day)
		pipe.HIncrBy(ctx, key, "checked", n)
		for f, c := range b.bad[k] {
			pipe.HIncrBy(ctx, key, f, c)
		}
		pipe.Expire(ctx, key, dayTTL)
		if ss := b.samples[k]; len(ss) > 0 {
			sk := samplesKey(k.tenant, k.name)
			for _, s := range ss {
				raw, _ := json.Marshal(s)
				pipe.LPush(ctx, sk, raw)
			}
			pipe.LTrim(ctx, sk, 0, maxSamples-1)
			pipe.Expire(ctx, sk, sampleTTL)
		}
	}
	_, err := pipe.Exec(ctx)
	return err
}

// Refused counts events the server API refused at `at` (enforce mode), with their problems, and
// keeps them as samples like Batch does.
func Refused(ctx context.Context, rdb redis.Cmdable, s *Schema, e *event.Event, vs []Violation, at time.Time) error {
	key := dayKey(e.TenantID, e.Name, at.UTC().Format(dayLayout))
	pipe := rdb.Pipeline()
	pipe.HIncrBy(ctx, key, "refused", 1)
	for _, v := range vs {
		pipe.HIncrBy(ctx, key, v.Code+":"+v.Prop, 1)
	}
	pipe.Expire(ctx, key, dayTTL)
	raw, _ := json.Marshal(Sample{At: at, EventID: e.ID, ExternalID: e.ExternalID, Violations: vs, Props: redact(s, e.Props)})
	sk := samplesKey(e.TenantID, e.Name)
	pipe.LPush(ctx, sk, raw)
	pipe.LTrim(ctx, sk, 0, maxSamples-1)
	pipe.Expire(ctx, sk, sampleTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// redact returns props as JSON without the schema's personal properties, cut at maxSample bytes.
func redact(s *Schema, props map[string]any) string {
	raw, _ := json.Marshal(props)
	if paths := s.PersonalPaths(); len(paths) > 0 {
		var copyProps map[string]any
		_ = json.Unmarshal(raw, &copyProps)
		for _, p := range paths {
			remove(copyProps, p)
		}
		raw, _ = json.Marshal(copyProps)
	}
	if len(raw) > maxSample {
		return `{"note":"too large to keep"}`
	}
	return string(raw)
}

// remove deletes a path (items[] paths in every item) from props.
func remove(props map[string]any, path string) {
	p, ok := segment.ParsePath(path)
	if !ok {
		return
	}
	del := func(m map[string]any, keys []string) {
		for i, k := range keys {
			if i == len(keys)-1 {
				delete(m, k)
				return
			}
			next, ok := m[k].(map[string]any)
			if !ok {
				return
			}
			m = next
		}
	}
	if !p.List {
		del(props, p.Keys)
		return
	}
	if len(p.InItems) == 0 {
		del(props, p.Keys)
		return
	}
	list, _ := segment.Value(props, strings.Join(p.Keys, "."))
	items, _ := list.([]any)
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			del(m, p.InItems)
		}
	}
}

// Day is one day's checks of an event.
type Day struct {
	Day     string `json:"day"` // YYYY-MM-DD
	Checked int64  `json:"checked"`
	Bad     int64  `json:"bad"`
	Refused int64  `json:"refused"`
}

// Problem is how often one problem happened in the period.
type Problem struct {
	Prop  string `json:"prop"`
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

// Summary is an event's checks over the last days.
type Summary struct {
	Days     []Day     `json:"days"`
	Checked  int64     `json:"checked"`
	Bad      int64     `json:"bad"`
	Refused  int64     `json:"refused"`
	Problems []Problem `json:"problems"`
	Samples  []Sample  `json:"samples"`
}

// Summarize reads an event's checks for the days up to now (newest last) and its samples.
func Summarize(ctx context.Context, rdb redis.Cmdable, tenant int64, name string, days int, now time.Time) (Summary, error) {
	pipe := rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, days)
	dates := make([]time.Time, days)
	for i := 0; i < days; i++ {
		dates[i] = now.UTC().AddDate(0, 0, -(days - 1 - i))
		cmds[i] = pipe.HGetAll(ctx, dayKey(tenant, name, dates[i].Format(dayLayout)))
	}
	sampleCmd := pipe.LRange(ctx, samplesKey(tenant, name), 0, maxSamples-1)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return Summary{}, err
	}
	out := Summary{Days: make([]Day, days), Problems: []Problem{}, Samples: []Sample{}}
	problems := map[string]int64{}
	for i, c := range cmds {
		m := c.Val()
		d := Day{Day: dates[i].Format(time.DateOnly), Checked: atoi(m["checked"]), Bad: atoi(m["bad"]), Refused: atoi(m["refused"])}
		out.Days[i] = d
		out.Checked += d.Checked
		out.Bad += d.Bad
		out.Refused += d.Refused
		for f, v := range m {
			if strings.Contains(f, ":") {
				problems[f] += atoi(v)
			}
		}
	}
	for f, n := range problems {
		code, prop, _ := strings.Cut(f, ":")
		out.Problems = append(out.Problems, Problem{Prop: prop, Code: code, Count: n})
	}
	sort.Slice(out.Problems, func(i, j int) bool {
		if out.Problems[i].Count != out.Problems[j].Count {
			return out.Problems[i].Count > out.Problems[j].Count
		}
		return out.Problems[i].Prop+out.Problems[i].Code < out.Problems[j].Prop+out.Problems[j].Code
	})
	for _, raw := range sampleCmd.Val() {
		var s Sample
		if json.Unmarshal([]byte(raw), &s) == nil {
			out.Samples = append(out.Samples, s)
		}
	}
	return out, nil
}

// Totals returns, per event name, the problems counted (bad + refused) and checks in the last days.
func Totals(ctx context.Context, rdb redis.Cmdable, tenant int64, names []string, days int, now time.Time) (map[string][2]int64, error) {
	pipe := rdb.Pipeline()
	type ref struct {
		name string
		cmd  *redis.SliceCmd
	}
	var refs []ref
	for _, n := range names {
		for i := 0; i < days; i++ {
			d := now.UTC().AddDate(0, 0, -i).Format(dayLayout)
			refs = append(refs, ref{n, pipe.HMGet(ctx, dayKey(tenant, n, d), "checked", "bad", "refused")})
		}
	}
	if len(refs) == 0 {
		return map[string][2]int64{}, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	out := map[string][2]int64{}
	for _, r := range refs {
		v := r.cmd.Val()
		t := out[r.name]
		t[0] += atoi(str(v[1])) + atoi(str(v[2]))
		t[1] += atoi(str(v[0])) + atoi(str(v[2]))
		out[r.name] = t
	}
	return out, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func atoi(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
