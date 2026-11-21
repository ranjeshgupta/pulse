package pg

// Surveys and their responses (package survey).

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"pulse/internal/auth"
	"pulse/internal/model"
)

const surveyCols = `id, tenant_id, name, status, questions, intro, thank_you, public_key, created_at, updated_at`

func scanSurvey(r pgx.Row) (model.Survey, error) {
	var s model.Survey
	var qs []byte
	err := r.Scan(&s.ID, &s.TenantID, &s.Name, &s.Status, &qs, &s.Intro, &s.ThankYou, &s.PublicKey, &s.CreatedAt, &s.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(qs, &s.Questions)
	}
	return s, notFound(err)
}

func (s *Store) ListSurveys(ctx context.Context, tenant int64) ([]model.Survey, error) {
	rows, err := s.db.Query(ctx, `SELECT `+surveyCols+` FROM surveys WHERE tenant_id = $1 ORDER BY updated_at DESC`, tenant)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Survey, error) { return scanSurvey(r) })
	if out == nil {
		out = []model.Survey{}
	}
	return out, err
}

// GetSurvey returns a workspace's survey (public pages use SurveyByIDPublic).
func (s *Store) GetSurvey(ctx context.Context, tenant, id int64) (model.Survey, error) {
	return scanSurvey(s.db.QueryRow(ctx, `SELECT `+surveyCols+` FROM surveys WHERE id = $2 AND tenant_id = $1`, tenant, id))
}

// SurveyByIDPublic finds a survey by id alone, for the public survey pages (/s/{id}/...), whose
// signed links or public key say which workspace it belongs to. Nothing else may use it: every
// workspace route must call GetSurvey with its tenant.
func (s *Store) SurveyByIDPublic(ctx context.Context, id int64) (model.Survey, error) {
	return scanSurvey(s.db.QueryRow(ctx, `SELECT `+surveyCols+` FROM surveys WHERE id = $1`, id))
}

// SaveSurvey creates (ID 0) or updates a survey; a new one gets its public key.
func (s *Store) SaveSurvey(ctx context.Context, sv model.Survey) (int64, error) {
	qs, _ := json.Marshal(sv.Questions)
	if sv.ID == 0 {
		err := s.db.QueryRow(ctx, `INSERT INTO surveys (tenant_id, name, status, questions, intro, thank_you, public_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, sv.TenantID, sv.Name, sv.Status, qs, sv.Intro, sv.ThankYou, auth.NewToken("")[:24]).Scan(&sv.ID)
		return sv.ID, err
	}
	tag, err := s.db.Exec(ctx, `UPDATE surveys SET name = $3, status = $4, questions = $5, intro = $6, thank_you = $7, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, sv.TenantID, sv.ID, sv.Name, sv.Status, qs, sv.Intro, sv.ThankYou)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return sv.ID, err
}

func (s *Store) DeleteSurvey(ctx context.Context, tenant, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM surveys WHERE tenant_id = $1 AND id = $2`, tenant, id)
	if err == nil && tag.RowsAffected() == 0 {
		err = ErrNotFound
	}
	return err
}

// SaveSurveyResponse stores an answer: a contact's replaces their earlier one for the same message.
// changed is false when that earlier answer was the same (nothing was written).
func (s *Store) SaveSurveyResponse(ctx context.Context, r model.SurveyResponse) (changed bool, err error) {
	ans, _ := json.Marshal(r.Answers)
	if r.ExternalID == "" {
		_, err := s.db.Exec(ctx, `INSERT INTO survey_responses (survey_id, tenant_id, answers, nps) VALUES ($1, $2, $3, $4)`,
			r.SurveyID, r.TenantID, ans, r.NPS)
		return err == nil, err
	}
	err = s.db.QueryRow(ctx, `INSERT INTO survey_responses (survey_id, tenant_id, external_id, message_id, answers, nps)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (survey_id, external_id, message_id) WHERE external_id <> ''
		DO UPDATE SET answers = $5, nps = $6, updated_at = now()
		WHERE survey_responses.answers IS DISTINCT FROM excluded.answers OR survey_responses.nps IS DISTINCT FROM excluded.nps
		RETURNING true`, r.SurveyID, r.TenantID, r.ExternalID, r.MessageID, ans, r.NPS).Scan(&changed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return changed, err
}

// SurveyResponses are a survey's answers since since, newest first (at most limit).
func (s *Store) SurveyResponses(ctx context.Context, tenant, survey int64, since time.Time, limit int) ([]model.SurveyResponse, error) {
	rows, err := s.db.Query(ctx, `SELECT survey_id, tenant_id, external_id, message_id, answers, nps, updated_at FROM survey_responses
		WHERE tenant_id = $1 AND survey_id = $2 AND updated_at >= $3 ORDER BY updated_at DESC LIMIT $4`, tenant, survey, since, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.SurveyResponse, error) {
		var x model.SurveyResponse
		var ans []byte
		err := r.Scan(&x.SurveyID, &x.TenantID, &x.ExternalID, &x.MessageID, &ans, &x.NPS, &x.At)
		if err == nil {
			err = json.Unmarshal(ans, &x.Answers)
		}
		return x, err
	})
}
