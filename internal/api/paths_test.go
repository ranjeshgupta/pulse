package api

import (
	"testing"

	"pulse/internal/store/ch"
)

func TestPathLinks(t *testing.T) {
	got := pathLinks([]ch.PathRow{
		{Steps: []string{"signup", "view", "buy"}, People: 5},
		{Steps: []string{"signup", "view"}, People: 3},
		{Steps: []string{"signup", "search", "buy"}, People: 2},
	}, 2)
	want := map[pathLink]bool{
		{Step: 0, From: "signup", To: "view", People: 8}: true, {Step: 1, From: "view", To: "buy", People: 5}: true,
		{Step: 1, From: "view", To: "", People: 3}: true, {Step: 0, From: "signup", To: "search", People: 2}: true,
		{Step: 1, From: "search", To: "buy", People: 2}: true,
	}
	if len(got) != len(want) {
		t.Fatalf("links: %+v", got)
	}
	for _, l := range got {
		if !want[l] {
			t.Errorf("unexpected %+v", l)
		}
	}
}
