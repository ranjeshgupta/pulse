package analytics

// Journey A/B tests: how each path of a split step does. Contacts are counted on the path the
// split step sent them down (its journey_split events, recorded before a winner is decided), and
// "hits" are those who then opened or clicked a message of a send step after the split, or did the
// journey's goal event.

import (
	"errors"
	"time"

	"pulse/internal/event"
)

const (
	SplitOpened    = "opened"
	SplitClicked   = "clicked"
	SplitConverted = "converted"
)

// SplitQuery selects one split step's test.
type SplitQuery struct {
	Tenant  int64
	Journey int64
	Step    string
	Metric  string    // opened | clicked | converted; "" counts contacts per path only
	Nodes   []string  // opened, clicked: the send steps after the split
	Goal    string    // converted: the journey's goal event
	Since   time.Time // lower bound for the journey's events (its start)
}

// PathStat is one path's test result.
type PathStat struct {
	Path     int       `json:"path"`
	Entrants uint64    `json:"entrants"`
	Hits     uint64    `json:"hits"`
	Rate     float64   `json:"rate"`
	FirstAt  time.Time `json:"-"`
}

// SplitStatsSQL: (path, entrants, hits, first entry) per path that has entrants.
func SplitStatsSQL(q SplitQuery) (Query, error) {
	t := uint64(q.Tenant)
	since := q.Since.UTC()
	var hits string
	var hargs []any
	args := []any{t, event.JourneySplit, since, uint64(q.Journey), q.Step}
	assigned := "SELECT contact_id, toInt64(JSONExtractInt(props, 'path')) AS path, min(ts) AS at FROM events" +
		" WHERE tenant_id = ? AND name = ? AND ts >= ? AND JSONExtractUInt(props, 'journey_id') = ? AND JSONExtractString(props, 'step') = ?" +
		" AND NOT JSONExtractBool(props, 'decided') GROUP BY contact_id, path"
	switch q.Metric {
	case "":
		return Query{SQL: "SELECT path, count(), toUInt64(0), min(at) FROM (" + assigned + ") GROUP BY path ORDER BY path", Args: args}, nil
	case SplitOpened, SplitClicked:
		if len(q.Nodes) == 0 {
			return Query{}, errors.New("no send step after the split")
		}
		hits = "SELECT contact_id, max(ts) AS last FROM message_events WHERE tenant_id = ? AND source = 'journey' AND source_id = ?" +
			" AND kind = ? AND node IN (" + marks(len(q.Nodes)) + ") AND ts >= ? GROUP BY contact_id"
		hargs = []any{t, uint64(q.Journey), q.Metric}
		for _, n := range q.Nodes {
			hargs = append(hargs, n)
		}
		hargs = append(hargs, since)
	case SplitConverted:
		if !event.ValidName(q.Goal) {
			return Query{}, errors.New("the journey has no goal event")
		}
		hits = "SELECT contact_id, max(ts) AS last FROM events WHERE tenant_id = ? AND name = ? AND ts >= ? GROUP BY contact_id"
		hargs = []any{t, q.Goal, since}
	default:
		return Query{}, errors.New("unknown split metric")
	}
	args = append(args, hargs...)
	return Query{
		// Unmatched rows of the LEFT JOIN have last = 1970, which never counts as a hit.
		SQL: "SELECT a.path, count(), countIf(h.last >= a.at), min(a.at) FROM (" + assigned + ") AS a" +
			" LEFT JOIN (" + hits + ") AS h ON h.contact_id = a.contact_id" +
			" GROUP BY a.path ORDER BY a.path",
		Args: args,
	}, nil
}

// PickWinner chooses the winning path once every one of the split's paths has at least minEach
// entrants: the highest rate, then the most hits, then the first path. ok is false while the test
// still needs entrants.
func PickWinner(stats []PathStat, paths, minEach int) (winner int, ok bool) {
	by := map[int]PathStat{}
	for _, s := range stats {
		by[s.Path] = s
	}
	winner = -1
	var best PathStat
	for p := 0; p < paths; p++ {
		s := by[p]
		if s.Entrants < uint64(max(minEach, 1)) {
			return -1, false
		}
		r := Rate(s)
		if winner < 0 || r > Rate(best) || (r == Rate(best) && s.Hits > best.Hits) {
			winner, best = p, s
		}
	}
	return winner, winner >= 0
}

// Rate is hits per entrant.
func Rate(s PathStat) float64 {
	if s.Entrants == 0 {
		return 0
	}
	return float64(s.Hits) / float64(s.Entrants)
}
