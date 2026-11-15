package edgestack

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/mux"
	bolt "go.etcd.io/bbolt"
)

type summaryRecord struct {
	Summary   Summary        `json:"summary"`
	UserID    int            `json:"userId"`
	Request   SummaryRequest `json:"request"`
	CreatedAt time.Time      `json:"createdAt"`
}

func (s *Service) createSummary(w http.ResponseWriter, r *http.Request) error {
	principal, e := s.principal(r)
	if e != nil {
		return e
	}
	var request SummaryRequest
	if e := decode(w, r, &request); e != nil {
		return e
	}
	if request.DeviceID == "" || request.From.IsZero() || !request.From.Before(request.To) || request.To.Sub(request.From) > Retention || request.To.After(s.opts.Now().Add(time.Minute)) || utf8.RuneCountInString(request.Question) > 2000 {
		return fail(400, "invalid_summary_request")
	}
	if _, busy := s.summaryUsers.LoadOrStore(principal.UserID, true); busy {
		return fail(429, "summary_already_in_progress")
	}
	defer s.summaryUsers.Delete(principal.UserID)
	var events []Event
	var coverage Coverage
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		events, _, coverage, e = s.query(r, tx, request.DeviceID, request.From, request.To)
		return e
	})
	if err != nil {
		return err
	}
	evidence := make([]Evidence, 0)
	textBytes := len(request.Question)
	for _, event := range events {
		if event.Type == "voice.playback_state" {
			continue
		}
		textBytes += len(event.Text) + len(event.Provenance) + len(event.Phase)
		if len(evidence) >= 200 || textBytes > 64<<10 {
			return fail(400, "summary_range_too_large")
		}
		evidence = append(evidence, Evidence{Ref: "evt:" + event.EventID, EventID: event.EventID, Text: event.Text, OccurredAt: event.OccurredAt, Type: event.Type, Provenance: event.Provenance, Phase: event.Phase})
	}
	request.Coverage = coverage
	var secrets []string
	var redactionErr error
	if len(evidence) > 0 {
		if provider, ok := s.opts.Provider.(redactionSecretProvider); ok {
			secrets, redactionErr = provider.RedactionSecrets()
		}
	}
	request, evidence = redactSummaryInput(request, evidence, secrets)
	result := Summary{ID: newID(), Status: "no_data", Title: "交互摘要", Topics: []Topic{}, Uncertainties: []string{}, Coverage: coverage, Evidence: evidence}
	if len(evidence) == 0 {
		result.Title = "所选时间范围暂无记录"
		result.Summary = "所选时间范围内没有可用的对话或观察记录。"
		result.Uncertainties = append(result.Uncertainties, "没有记录不代表该时间范围没有发生交互。")
	}
	if len(evidence) > 0 {
		if redactionErr != nil {
			result.Status = "failed"
			result.ErrorCode = providerCode(redactionErr)
		} else if s.opts.Provider == nil {
			result.Status = "failed"
			result.ErrorCode = "provider_not_configured"
		} else {
			select {
			case s.summarySlots <- struct{}{}:
				defer func() { <-s.summarySlots }()
			default:
				return fail(429, "summary_capacity_reached")
			}
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			generated, err := s.opts.Provider.Summarize(ctx, request, evidence)
			cancel()
			if err != nil {
				result.Status = "failed"
				result.ErrorCode = providerCode(err)
			} else if err := validateSummary(generated, evidence); err != nil {
				result.Status = "failed"
				result.ErrorCode = "provider_invalid_evidence"
			} else {
				result.Status = "completed"
				result.Title = generated.Title
				result.Summary = generated.Summary
				result.Topics = nonNil(generated.Topics)
				result.Answer = generated.Answer
				if strings.TrimSpace(request.Question) == "" {
					result.Answer = ""
				}
				result.Uncertainties = nonNil(generated.Uncertainties)
			}
		}
	}
	if len(coverage.Gaps) > 0 {
		result.Uncertainties = append(result.Uncertainties, "数据来源或历史覆盖存在缺口，本摘要仅描述已记录的证据。")
	}
	record := summaryRecord{Summary: result, UserID: principal.UserID, Request: request, CreatedAt: s.opts.Now().UTC()}
	// Recheck every underlying event and current Portainer endpoint permissions
	// after the remote call, including revocations that occurred while it ran.
	err = s.db.Update(func(tx *bolt.Tx) error {
		if e := s.authorizeSummary(r, tx, record); e != nil {
			return e
		}
		return put(tx, "summaries", result.ID, record)
	})
	if err == nil {
		respond(w, 201, result)
	}
	return err
}

func validateSummary(result Summary, evidence []Evidence) error {
	if strings.TrimSpace(result.Title) == "" || strings.TrimSpace(result.Summary) == "" || len(result.Title) > 500 || len(result.Summary) > 16000 || len(result.Answer) > 16000 || len(result.Topics) == 0 || len(result.Topics) > 50 {
		return errors.New("invalid summary")
	}
	refs := map[string]bool{}
	for _, e := range evidence {
		refs[e.Ref] = true
	}
	for _, topic := range result.Topics {
		if strings.TrimSpace(topic.Text) == "" || len(topic.EventRefs) == 0 {
			return errors.New("missing evidence")
		}
		for _, ref := range topic.EventRefs {
			if !refs[ref] {
				return errors.New("invalid evidence")
			}
		}
	}
	return nil
}
func (s *Service) authorizeSummary(r *http.Request, tx *bolt.Tx, record summaryRecord) error {
	p, e := s.principal(r)
	if e != nil {
		return e
	}
	if !p.Admin && p.UserID != record.UserID {
		return fail(404, "not_found")
	}
	if _, e := s.authorizedDevice(r, tx, record.Request.DeviceID); e != nil {
		return e
	}
	for _, evidence := range record.Summary.Evidence {
		event, e := read[Event](tx, "events", evidence.EventID)
		if e != nil || event.DeviceID != record.Request.DeviceID || event.OccurredAt.Before(s.opts.Now().Add(-Retention)) {
			return fail(404, "summary_evidence_unavailable")
		}
		if _, e := s.authorizedTask(r, tx, event.TaskID); e != nil {
			return fail(404, "summary_evidence_unavailable")
		}
	}
	return nil
}
func (s *Service) getSummary(w http.ResponseWriter, r *http.Request) error {
	var record summaryRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		record, e = read[summaryRecord](tx, "summaries", mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		return s.authorizeSummary(r, tx, record)
	})
	if err == nil {
		respond(w, 200, record.Summary)
	}
	return err
}
