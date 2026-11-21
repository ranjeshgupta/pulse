package api

// Reusable content blocks (O6): /v1/content-blocks. A block is used as {{block.key}} in any
// template; sends and previews put its current content in.

import (
	"context"
	"net/http"
	"strings"

	"pulse/internal/channel"
	"pulse/internal/model"
	"pulse/internal/personalize"
	"pulse/internal/spamcheck"
)

// BlockStore keeps content blocks (pg.Store).
type BlockStore interface {
	ListBlocks(ctx context.Context, tenant int64) ([]model.ContentBlock, error)
	GetBlock(ctx context.Context, tenant, id int64) (model.ContentBlock, error)
	SaveBlock(ctx context.Context, b model.ContentBlock) (int64, error)
	DeleteBlock(ctx context.Context, tenant, id int64) error
	TemplatesUsingBlock(ctx context.Context, tenant int64, key string) ([]model.Template, error)
}

const maxBlocks = 200

func (s *Server) blockRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/content-blocks", s.tenant(s.listBlocks))
	mux.HandleFunc("POST /v1/content-blocks", s.guardBlockEdit(s.saveBlock))
	mux.HandleFunc("PUT /v1/content-blocks/{id}", s.guardBlockEdit(s.saveBlock))
	mux.HandleFunc("DELETE /v1/content-blocks/{id}", s.guardBlockEdit(s.deleteBlock))
	mux.HandleFunc("GET /v1/content-blocks/{id}/usage", s.tenant(s.blockUsage))
}

func (s *Server) blockStore(w http.ResponseWriter) (BlockStore, bool) {
	bs, ok := s.Store.(BlockStore)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "content blocks aren't available")
	}
	return bs, ok
}

// renderBlocks are the workspace's blocks for rendering (nil when there are none or no store).
func (s *Server) renderBlocks(ctx context.Context, tenant int64) map[string]channel.Block {
	bs, ok := s.Store.(BlockStore)
	if !ok {
		return nil
	}
	list, err := bs.ListBlocks(ctx, tenant)
	if err != nil || len(list) == 0 {
		return nil
	}
	out := make(map[string]channel.Block, len(list))
	for _, b := range list {
		text := b.Text
		if strings.TrimSpace(text) == "" {
			text = spamcheck.Text(b.HTML)
		}
		out[b.Key] = channel.Block{HTML: b.HTML, Text: text}
	}
	return out
}

func (s *Server) listBlocks(w http.ResponseWriter, r *http.Request, tenant int64) {
	bs, ok := s.blockStore(w)
	if !ok {
		return
	}
	list, err := bs.ListBlocks(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if list == nil {
		list = []model.ContentBlock{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocks": list})
}

func (s *Server) saveBlock(w http.ResponseWriter, r *http.Request, tenant int64) {
	bs, ok := s.blockStore(w)
	if !ok {
		return
	}
	var b model.ContentBlock
	if !decode(w, r, &b) {
		return
	}
	b.TenantID, b.Key, b.Name = tenant, normalizeBlockKey(b.Key), strings.TrimSpace(b.Name)
	if id := r.PathValue("id"); id != "" {
		n, ok := pathID(w, r)
		if !ok {
			return
		}
		b.ID = n
	}
	switch {
	case !personalize.BlockKeyRe.MatchString(b.Key):
		writeErr(w, http.StatusBadRequest, "key: lower-case letters, digits and _ (used as {{block.key}})")
		return
	case b.Key == "feed" || b.Key == "recommendations":
		writeErr(w, http.StatusBadRequest, "key: {{block."+b.Key+"}} is filled by Pulse (RSS feed or recommended products); pick another")
		return
	case b.Name == "" || len(b.Name) > 120:
		writeErr(w, http.StatusBadRequest, "name: 1 to 120 characters")
		return
	case len(b.HTML) > 200_000 || len(b.Text) > 20_000:
		writeErr(w, http.StatusBadRequest, "a block is at most 200 KB of HTML and 20 KB of text")
		return
	case strings.Contains(b.HTML+b.Text, "block."):
		if len(personalize.BlockKeys(b.HTML+b.Text)) > 0 {
			writeErr(w, http.StatusBadRequest, "a block can't contain other blocks")
			return
		}
	}
	for _, src := range []string{b.HTML, b.Text} {
		if err := checkTemplateSyntax(src); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if b.ID == 0 {
		if list, err := bs.ListBlocks(r.Context(), tenant); err == nil && len(list) >= maxBlocks {
			writeErr(w, http.StatusBadRequest, "at most 200 content blocks")
			return
		}
	}
	id, err := bs.SaveBlock(r.Context(), b)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) blockUsage(w http.ResponseWriter, r *http.Request, tenant int64) {
	bs, ok := s.blockStore(w)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, err := bs.GetBlock(r.Context(), tenant, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	ts, err := bs.TemplatesUsingBlock(r.Context(), tenant, b.Key)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]map[string]any, len(ts))
	for i, t := range ts {
		out[i] = map[string]any{"id": t.ID, "name": t.Name, "channel": t.Channel}
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": out})
}

func (s *Server) deleteBlock(w http.ResponseWriter, r *http.Request, tenant int64) {
	bs, ok := s.blockStore(w)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := bs.DeleteBlock(r.Context(), tenant, id); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkTemplateSyntax reports a template text that wouldn't render.
func checkTemplateSyntax(src string) error {
	if !strings.Contains(src, "{{") {
		return nil
	}
	_, err := personalize.New(personalize.Vars{}).Text("block", src, nil)
	return err
}
