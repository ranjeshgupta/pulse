package api

// GET /v1/revenue/customer-value: buyers by customer-value group (package rfm) and churn risk, with
// their revenue, average orders and days since the last purchase. Groups come from the contacts'
// rfm_group / churn_risk attributes, set by the daily scoring pass.

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"pulse/internal/rfm"
	"pulse/internal/store/ch"
)

// CustomerValueReporter builds the report (ch.Store).
type CustomerValueReporter interface {
	RFMReport(ctx context.Context, tenant int64, ev, amountProp string, now time.Time) ([]ch.RFMGroupRow, error)
}

// groupNotes say what each group is and what to do with it.
var groupNotes = map[string]string{
	rfm.Champions:     "Bought recently, buy often and spend the most. Reward them; ask for reviews and referrals.",
	rfm.Loyal:         "Buy often. Offer loyalty perks and upsell.",
	rfm.Potential:     "Recent buyers with a few orders. Offer membership or a reason to come back.",
	rfm.New:           "First purchase just now. Welcome and onboard them.",
	rfm.Promising:     "Recent, but bought once. Build the habit with a timely follow-up.",
	rfm.NeedAttention: "Middling recency and frequency. Limited-time offers and recommendations.",
	rfm.AboutToSleep:  "Haven't bought for a while and bought little. Re-engage before they lapse.",
	rfm.AtRisk:        "Used to buy often, not lately. Win them back with something personal.",
	rfm.CantLose:      "Were among the best, gone quiet. Reach out now, by more than one channel.",
	rfm.Hibernating:   "Long gone, bought little. Low-cost reminders only.",
	rfm.Lost:          "The longest gone. Leave them or try one last win-back.",
}

// refreshCustomerValue starts scoring the workspace's buyers now (at most every 5 minutes), as the
// daily pass would; it answers 202 at once and groups update as the changes are processed.
func (s *Server) refreshCustomerValue(w http.ResponseWriter, r *http.Request, tenant int64) {
	src, ok := s.Analytics.(rfm.Source)
	if !ok || s.RDB == nil || s.Pub == nil {
		writeErr(w, http.StatusServiceUnavailable, "scoring needs the analytics database, Redis and Kafka")
		return
	}
	rs, err := s.revenueSettings(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	// One run per workspace at a time, however long it takes (the 5-minute key only spaces starts);
	// the lock is kept alive while the pass runs, so a crash frees it within minutes.
	release, ok := rfm.Hold(r.Context(), s.RDB, "rfmrun:"+strconv.FormatInt(tenant, 10), rfm.LockTTL)
	if !ok {
		writeErr(w, http.StatusConflict, "customer value is being scored right now; it updates as it goes")
		return
	}
	if ok, err := s.RDB.SetNX(r.Context(), "rfmnow:"+strconv.FormatInt(tenant, 10), 1, 5*time.Minute).Result(); err != nil {
		release()
		s.fail(w, err)
		return
	} else if !ok {
		release()
		writeErr(w, http.StatusTooManyRequests, "customer value was scored in the last 5 minutes; try again later")
		return
	}
	// In the background: a big workspace's first pass changes every buyer, and its events go out
	// at a steady rate (rfm.Throttled) so they never crowd out live ones.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		defer release()
		started := s.now()
		n, changed, err := rfm.Refresh(ctx, src, s.RDB, rfm.Throttled(s.publishEvents, rfm.DefaultPerSec), tenant, rs.Event, rs.AmountProp, started)
		if err != nil {
			s.Log.Error("customer value", "tenant", tenant, "err", err)
			return
		}
		_ = rfm.Passed(ctx, s.RDB, tenant, started)
		s.Log.Info("customer value scored", "tenant", tenant, "buyers", n, "changed", changed)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

func (s *Server) customerValue(w http.ResponseWriter, r *http.Request, tenant int64) {
	cv, ok := s.Analytics.(CustomerValueReporter)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "reports need the analytics database")
		return
	}
	rs, err := s.revenueSettings(r.Context(), tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	rows, err := cv.RFMReport(r.Context(), tenant, rs.Event, rs.AmountProp, s.now())
	if err != nil {
		s.fail(w, err)
		return
	}
	if rows == nil {
		rows = []ch.RFMGroupRow{}
	}
	groups := make([]map[string]string, 0, len(rfm.Groups))
	for _, g := range rfm.Groups {
		groups = append(groups, map[string]string{"name": g, "note": groupNotes[g]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": rs, "rows": rows, "groups": groups,
		"risks": []string{rfm.RiskLow, rfm.RiskMedium, rfm.RiskHigh, rfm.RiskLost}})
}
