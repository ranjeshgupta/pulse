// Package survey is surveys and NPS (O6): a survey's questions (an NPS 0-10, a 1-5 rating, a
// choice or free text), answered on a hosted page reached from a message ({{survey.ID}}, one link
// per contact) or a public link. Each answer is stored with the survey and recorded on the
// contact as survey_answered (props survey_id, nps, answers…) for segments and journeys.
package survey

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"pulse/internal/model"
)

// Question types.
const (
	NPS    = "nps"    // 0-10: how likely to recommend
	Rating = "rating" // 1-5
	Choice = "choice" // one of options
	Text   = "text"
)

// Statuses.
const (
	Draft  = "draft"
	Live   = "live"
	Closed = "closed"
)

const (
	MaxQuestions = 10
	MaxText      = 2000
)

var qidRe = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

// Validate checks a survey before it is saved.
func Validate(s model.Survey) error {
	var errs []string
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 120 {
		errs = append(errs, "name: 1 to 120 characters")
	}
	if s.Status != Draft && s.Status != Live && s.Status != Closed {
		errs = append(errs, "status: draft, live or closed")
	}
	if len(s.Questions) == 0 || len(s.Questions) > MaxQuestions {
		errs = append(errs, fmt.Sprintf("ask 1 to %d questions", MaxQuestions))
	}
	seen := map[string]bool{}
	npsCount := 0
	for i, q := range s.Questions {
		at := fmt.Sprintf("question %d: ", i+1)
		if !qidRe.MatchString(q.ID) || seen[q.ID] {
			errs = append(errs, at+"each needs its own id (lower-case letters, digits, _)")
		}
		seen[q.ID] = true
		if strings.TrimSpace(q.Text) == "" || len(q.Text) > 500 {
			errs = append(errs, at+"the question is 1 to 500 characters")
		}
		switch q.Type {
		case NPS:
			npsCount++
		case Rating, Text:
		case Choice:
			if len(q.Options) < 2 || len(q.Options) > 12 {
				errs = append(errs, at+"a choice needs 2 to 12 options")
			}
			for _, o := range q.Options {
				if strings.TrimSpace(o) == "" || len(o) > 200 {
					errs = append(errs, at+"options are 1 to 200 characters")
					break
				}
			}
		default:
			errs = append(errs, at+"type is nps, rating, choice or text")
		}
	}
	if npsCount > 1 {
		errs = append(errs, "ask the NPS question once")
	}
	if len(s.ThankYou) > 1000 {
		errs = append(errs, "thank-you message: at most 1000 characters")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Answers reads a submitted form into answers by question id (scores as numbers), with the
// problems to show next to each question.
func Answers(s model.Survey, form map[string]string) (map[string]any, map[string]string) {
	out, problems := map[string]any{}, map[string]string{}
	for _, q := range s.Questions {
		v := strings.TrimSpace(form["q_"+q.ID])
		if v == "" {
			if q.Required {
				problems[q.ID] = "Please answer this one."
			}
			continue
		}
		switch q.Type {
		case NPS, Rating:
			n, err := strconv.Atoi(v)
			lo, hi := 0, 10
			if q.Type == Rating {
				lo, hi = 1, 5
			}
			if err != nil || n < lo || n > hi {
				problems[q.ID] = fmt.Sprintf("Pick %d to %d.", lo, hi)
				continue
			}
			out[q.ID] = n
		case Choice:
			ok := false
			for _, o := range q.Options {
				if o == v {
					ok = true
				}
			}
			if !ok {
				problems[q.ID] = "Pick one of the options."
				continue
			}
			out[q.ID] = v
		case Text:
			if len(v) > MaxText {
				v = v[:MaxText]
			}
			out[q.ID] = v
		}
	}
	return out, problems
}

// NPSQuestion is the survey's NPS question id ("" without one).
func NPSQuestion(s model.Survey) string {
	for _, q := range s.Questions {
		if q.Type == NPS {
			return q.ID
		}
	}
	return ""
}

// Group names an NPS score: promoter (9-10), passive (7-8) or detractor (0-6).
func Group(score int) string {
	switch {
	case score >= 9:
		return "promoter"
	case score >= 7:
		return "passive"
	}
	return "detractor"
}

// Score is the Net Promoter Score of a 0-10 distribution: % promoters − % detractors (-100..100).
func Score(dist [11]int) (nps float64, n int) {
	var pro, det int
	for s, c := range dist {
		n += c
		if s >= 9 {
			pro += c
		} else if s <= 6 {
			det += c
		}
	}
	if n == 0 {
		return 0, 0
	}
	return float64(pro-det) * 100 / float64(n), n
}
