package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnknownZoneIsBadRequest(t *testing.T) {
	s, _, _ := newServer()
	chErr := errors.New("code: 36, message: Cannot load time zone America/Ciudad_Juarez: In scope SELECT toStartOfDay(ts, 'America/Ciudad_Juarez')")
	w := httptest.NewRecorder()
	s.failReport(w, chErr)
	if w.Code != http.StatusBadRequest || !unknownZone(chErr) || unknownZone(errors.New("timeout")) {
		t.Errorf("unknown zone: %d %s", w.Code, w.Body)
	}
}
