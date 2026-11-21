package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"pulse/internal/model"
	"pulse/internal/safehttp"
)

func TestArchiveSanitizedAndCached(t *testing.T) {
	s, _, _ := newServer()
	mr := miniredis.RunT(t)
	s.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	tpl := model.Template{ID: 3, Channel: "email", Subject: "News",
		HTML: `<html><head><meta http-equiv="refresh" content="0;url=https://evil.test"></head><body><p>Hi</p><script>x()</script><a href="javascript:x()">a</a></body></html>`}
	e := model.ArchiveEntry{RunID: 9, BroadcastID: 2, Name: "News", TemplateID: 3}
	a, err := s.renderArchived(context.Background(), 1, e, nil, map[int64]model.Template{3: tpl})
	if err != nil || strings.Contains(a.HTML, "refresh") || strings.Contains(a.HTML, "<script") || strings.Contains(a.HTML, "javascript:") || !strings.Contains(a.HTML, "<p>Hi</p>") {
		t.Fatalf("archive html: %v %s", err, a.HTML)
	}
	// Kept for a few minutes: the template isn't rendered again.
	tpl.HTML = "<p>changed</p>"
	if b, _ := s.renderArchived(context.Background(), 1, e, nil, map[int64]model.Template{3: tpl}); b.HTML != a.HTML {
		t.Errorf("not cached: %s", b.HTML)
	}
	if csp := archiveCSP("n"); !strings.Contains(csp, "frame-src 'none'") || strings.Contains(csp, "frame-src 'self'") {
		t.Errorf("csp: %s", csp)
	}
}

func TestFeedFetchStatus(t *testing.T) {
	if feedFetchStatus(fmt.Errorf("couldn't fetch the feed: %w", safehttp.ErrBlocked)) != http.StatusBadRequest ||
		feedFetchStatus(errors.New("the feed answered HTTP 500")) != http.StatusBadGateway {
		t.Error("feed fetch status")
	}
}
