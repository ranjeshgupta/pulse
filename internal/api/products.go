package api

// The product catalog and recommendations (O6, package catalog).
//
//	GET    /v1/products?q=&limit=&offset=        the catalog, newest first
//	PUT    /v1/products {"products": [...]}      add or replace up to 1000 by code
//	DELETE /v1/products/{code}
//	POST   /v1/products/refresh-lists            work out popular / trending / bought together now
//	GET    /v1/contacts/{external_id}/recommendations?n=4   what a message would show them now

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pulse/internal/catalog"
	"pulse/internal/model"
)

// ProductStore keeps the catalog (pg.Store).
type ProductStore interface {
	UpsertProducts(ctx context.Context, tenant int64, ps []model.Product) error
	ListProducts(ctx context.Context, tenant int64, q string, limit, offset int) ([]model.Product, int, error)
	ProductsByCode(ctx context.Context, tenant int64, codes []string) (map[string]model.Product, error)
	DeleteProduct(ctx context.Context, tenant int64, code string) error
}

// RecoSource works out the daily lists (ch.Store).
type RecoSource interface {
	RecoLists(ctx context.Context, tenant int64, buyEvent string, now time.Time) (catalog.Lists, error)
}

func (s *Server) productRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/products", s.tenant(s.listProducts))
	mux.HandleFunc("PUT /v1/products", s.tenant(s.putProducts))
	mux.HandleFunc("DELETE /v1/products/{code}", s.tenant(s.deleteProduct))
	mux.HandleFunc("POST /v1/products/refresh-lists", s.tenant(s.refreshRecoLists))
	mux.HandleFunc("GET /v1/contacts/{external_id}/recommendations", s.tenant(s.contactRecommendations))
}

func (s *Server) productStore(w http.ResponseWriter) (ProductStore, bool) {
	ps, ok := s.Store.(ProductStore)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "the product catalog isn't available")
	}
	return ps, ok
}

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request, tenant int64) {
	ps, ok := s.productStore(w)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, total, err := ps.ListProducts(r.Context(), tenant, r.URL.Query().Get("q"), limit, max(offset, 0))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": list, "total": total})
}

// maxProductsBody is PUT /v1/products' body limit (bodyLimit): catalog.MaxBatch products with
// descriptions and images fit.
const maxProductsBody = 8 << 20

func (s *Server) putProducts(w http.ResponseWriter, r *http.Request, tenant int64) {
	ps, ok := s.productStore(w)
	if !ok {
		return
	}
	var req struct {
		Products []model.Product `json:"products"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Products) == 0 || len(req.Products) > catalog.MaxBatch {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("send 1 to %d products at a time", catalog.MaxBatch))
		return
	}
	var problems []string
	seen := map[string]bool{}
	for i := range req.Products {
		p := &req.Products[i]
		p.Code = strings.TrimSpace(p.Code)
		if err := catalog.Validate(*p); err != nil {
			problems = append(problems, fmt.Sprintf("row %d: %v", i+1, err))
		} else if seen[p.Code] {
			problems = append(problems, fmt.Sprintf("row %d: %s is in the list twice", i+1, p.Code))
		}
		seen[p.Code] = true
		if len(problems) == 20 {
			break
		}
	}
	if len(problems) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "some products need fixing; nothing was saved", "problems": problems})
		return
	}
	if err := ps.UpsertProducts(r.Context(), tenant, req.Products); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": len(req.Products)})
}

func (s *Server) deleteProduct(w http.ResponseWriter, r *http.Request, tenant int64) {
	ps, ok := s.productStore(w)
	if !ok {
		return
	}
	if err := ps.DeleteProduct(r.Context(), tenant, r.PathValue("code")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) refreshRecoLists(w http.ResponseWriter, r *http.Request, tenant int64) {
	src, ok := s.Analytics.(RecoSource)
	if !ok || s.RDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "recommendations need the analytics database and Redis")
		return
	}
	rs, err := s.revenueSettings(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if ok, err := s.RDB.SetNX(r.Context(), "reconow:"+strconv.FormatInt(tenant, 10), 1, 5*time.Minute).Result(); err != nil {
		s.fail(w, err)
		return
	} else if !ok {
		writeErr(w, http.StatusTooManyRequests, "the lists were worked out in the last 5 minutes; try again later")
		return
	}
	l, err := src.RecoLists(r.Context(), tenant, rs.Event, s.now())
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(l.Popular)+len(l.Trending) > 0 { // nothing found: keep the lists there are, as the daily pass does
		if err := catalog.SaveLists(r.Context(), s.RDB, tenant, l); err != nil {
			s.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"popular": len(l.Popular), "trending": len(l.Trending), "bought_together": len(l.Also)})
}

func (s *Server) contactRecommendations(w http.ResponseWriter, r *http.Request, tenant int64) {
	ps, ok := s.productStore(w)
	if !ok {
		return
	}
	if s.RDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "recommendations need Redis")
		return
	}
	c, ok := s.knownContact(w, r, tenant, r.PathValue("external_id"))
	if !ok {
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 {
		n = catalog.DefaultN
	}
	rs, err := s.revenueSettings(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	recent, err := catalog.ReadRecent(r.Context(), s.RDB, tenant, c.ID, rs.Event)
	if err != nil {
		s.fail(w, err)
		return
	}
	picks, last, err := catalog.Recommend(r.Context(), s.RDB, ps.ProductsByCode, tenant, c.ID, rs.Event, n)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": picks, "last_viewed": last, "bought": recent.Bought, "viewed": recent.Viewed})
}
