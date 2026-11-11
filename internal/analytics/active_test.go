package analytics

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestActiveConditionMatchesMigration(t *testing.T) {
	b, err := os.ReadFile("../../migrations/clickhouse/006_delivery_and_dashboards.sql")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "WHERE "+ActiveCondition); n != 4 {
		t.Errorf("migration 006 uses the active condition %d times, want 4 (two backfills, two views)", n)
	}
}

func TestFillUsersAndStickiness(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 2)
	days := FillUsers(from, to, map[string]UsersDay{
		"2026-11-01": {DAU: 10, WAU: 10, MAU: 10},
		"2026-11-03": {DAU: 4, WAU: 12, MAU: 20},
	}, map[string]uint64{"2026-11-01": 10, "2026-11-03": 6})
	if len(days) != 3 || days[1].Day != "2026-11-02" || days[1].DAU != 0 {
		t.Fatalf("days: %+v", days)
	}
	if days[0].New != 10 || days[0].Returning != 0 || days[0].Sticky != 1 {
		t.Errorf("day 1: %+v", days[0])
	}
	if days[2].New != 4 || days[2].Returning != 0 || days[2].Sticky != 0.2 {
		t.Errorf("new users can't exceed DAU: %+v", days[2])
	}
	// avg DAU (10+0+4)/3 over the last MAU 20
	if s := Stickiness(days); math.Abs(s-14.0/3/20) > 1e-9 {
		t.Errorf("stickiness %v", s)
	}
	if Stickiness(nil) != 0 || Ratio(1, 0) != 0 {
		t.Error("empty")
	}
}
