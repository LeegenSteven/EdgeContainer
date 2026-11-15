package edgestack

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/gorilla/mux"
	bolt "go.etcd.io/bbolt"
)

type credentialRecord struct {
	Credential Credential `json:"credential"`
	Hash       string     `json:"hash"`
}

func (s *Service) createCredential(w http.ResponseWriter, r *http.Request) error {
	if e := s.admin(r); e != nil {
		return e
	}
	var c Credential
	if e := decode(w, r, &c); e != nil {
		return e
	}
	if c.Name == "" || len(c.DeviceIDs) == 0 || len(c.TaskIDs) == 0 || len(c.SourceIDs) == 0 {
		return fail(400, "explicit_scopes_required")
	}
	if len(c.Capabilities) == 0 {
		c.Capabilities = []string{"ingest-events", "ingest-status"}
	}
	for _, cap := range c.Capabilities {
		if !contains([]string{"ingest-events", "ingest-status", "observe-model"}, cap) {
			return fail(400, "invalid_capability")
		}
	}
	c.ID = newID()
	c.Revoked = false
	token := "es_ingest_" + newID() + newID()
	hash := sha256.Sum256([]byte(token))
	err := s.db.Update(func(tx *bolt.Tx) error {
		for _, id := range c.SourceIDs {
			source, e := read[Source](tx, "sources", id)
			if e != nil || !contains(c.DeviceIDs, source.DeviceID) || !contains(c.TaskIDs, source.TaskID) {
				return fail(400, "invalid_scope")
			}
		}
		for _, id := range c.DeviceIDs {
			if _, e := read[Device](tx, "devices", id); e != nil {
				return fail(400, "invalid_scope")
			}
		}
		for _, id := range c.TaskIDs {
			task, e := read[Task](tx, "tasks", id)
			if e != nil || !contains(c.DeviceIDs, task.DeviceID) {
				return fail(400, "invalid_scope")
			}
		}
		return put(tx, "credentials", c.ID, credentialRecord{c, hex.EncodeToString(hash[:])})
	})
	if err == nil {
		respond(w, 201, map[string]any{"id": c.ID, "token": token, "credential": c})
	}
	return err
}
func (s *Service) credentials(w http.ResponseWriter, r *http.Request) error {
	if e := s.admin(r); e != nil {
		return e
	}
	items := []Credential{}
	err := s.db.View(func(tx *bolt.Tx) error {
		rows, e := list[credentialRecord](tx, "credentials")
		for _, c := range rows {
			items = append(items, c.Credential)
		}
		return e
	})
	if err == nil {
		respond(w, 200, map[string]any{"items": items})
	}
	return err
}
func (s *Service) revokeCredential(w http.ResponseWriter, r *http.Request) error {
	if e := s.admin(r); e != nil {
		return e
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		c, e := read[credentialRecord](tx, "credentials", mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		c.Credential.Revoked = true
		return put(tx, "credentials", c.Credential.ID, c)
	})
	if err == nil {
		respond(w, 200, map[string]bool{"revoked": true})
	}
	return err
}
func ingestionCredential(tx *bolt.Tx, r *http.Request, capability string) (Credential, error) {
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer es_ingest_") {
		return Credential{}, fail(401, "ingest_credential_required")
	}
	hash := sha256.Sum256([]byte(strings.TrimPrefix(value, "Bearer ")))
	hashText := hex.EncodeToString(hash[:])
	rows, err := list[credentialRecord](tx, "credentials")
	if err != nil {
		return Credential{}, err
	}
	for _, c := range rows {
		if subtle.ConstantTimeCompare([]byte(c.Hash), []byte(hashText)) == 1 && !c.Credential.Revoked {
			if !contains(c.Credential.Capabilities, capability) {
				return Credential{}, fail(403, "ingest_scope_denied")
			}
			return c.Credential, nil
		}
	}
	return Credential{}, fail(401, "invalid_ingest_credential")
}
func scopedSource(tx *bolt.Tx, c Credential, sourceID, taskID, deviceID string) (Source, error) {
	source, err := read[Source](tx, "sources", sourceID)
	if err != nil {
		return source, fail(403, "ingest_scope_denied")
	}
	if !source.Enabled || !contains(c.SourceIDs, source.ID) || !contains(c.TaskIDs, source.TaskID) || !contains(c.DeviceIDs, source.DeviceID) || source.TaskID != taskID || (deviceID != "" && source.DeviceID != deviceID) {
		return source, fail(403, "ingest_scope_denied")
	}
	task, err := read[Task](tx, "tasks", source.TaskID)
	if err != nil || task.DeviceID != source.DeviceID {
		return source, fail(403, "ingest_scope_denied")
	}
	return source, nil
}

func (s *Service) ingestEvents(w http.ResponseWriter, r *http.Request) error {
	var payload struct {
		Events []Event `json:"events"`
	}
	if e := decode(w, r, &payload); e != nil {
		return e
	}
	if len(payload.Events) == 0 || len(payload.Events) > 500 {
		return fail(400, "invalid_batch_size")
	}
	accepted, duplicates := 0, 0
	now := s.opts.Now().UTC()
	err := s.db.Update(func(tx *bolt.Tx) error {
		c, e := ingestionCredential(tx, r, "ingest-events")
		if e != nil {
			return e
		}
		for _, event := range payload.Events {
			source, e := scopedSource(tx, c, event.SourceID, event.TaskID, event.DeviceID)
			if e != nil {
				return e
			}
			event.DeviceID = source.DeviceID
			if event.SchemaVersion != 1 || !validID(event.EventID) || event.SessionID == "" || event.Sequence < 0 || len(event.Text) > 16384 || !contains([]string{"voice.user_utterance", "voice.assistant_reply", "camera.observation", "voice.playback_state"}, event.Type) || event.OccurredAt.IsZero() || event.OccurredAt.Before(now.Add(-Retention)) || event.OccurredAt.After(now.Add(5*time.Minute)) {
				return fail(400, "invalid_event")
			}
			if event.Type != "voice.playback_state" && strings.TrimSpace(event.Text) == "" {
				return fail(400, "event_text_required")
			}
			if (strings.HasPrefix(event.Type, "voice.") && !strings.HasPrefix(source.Type, "voice")) || (event.Type == "camera.observation" && !strings.HasPrefix(source.Type, "camera")) {
				return fail(403, "source_type_mismatch")
			}
			event.ReceivedAt = time.Time{}
			if old, e := read[Event](tx, "events", event.EventID); e == nil {
				old.ReceivedAt = time.Time{}
				if !reflect.DeepEqual(old, event) {
					return fail(409, "event_id_conflict")
				}
				duplicates++
				continue
			}
			event.ReceivedAt = now
			if e := put(tx, "events", event.EventID, event); e != nil {
				return e
			}
			accepted++
			if event.OccurredAt.After(source.LastEventAt) {
				source.LastEventAt = event.OccurredAt
			}
			source.LastSeenAt = now
			source.Status = "connected"
			source.ErrorCode = ""
			if e := put(tx, "sources", source.ID, source); e != nil {
				return e
			}
		}
		return nil
	})
	if err == nil {
		respond(w, 200, map[string]int{"accepted": accepted, "duplicates": duplicates})
	}
	return err
}

type observation struct {
	OperationID string    `json:"operationId"`
	Slot        string    `json:"slot"`
	PackageID   string    `json:"packageId"`
	Revision    int       `json:"revision"`
	State       string    `json:"state"`
	ErrorCode   string    `json:"errorCode,omitempty"`
	ModelID     string    `json:"modelId,omitempty"`
	ModelHash   string    `json:"modelHash,omitempty"`
	ObservedAt  time.Time `json:"observedAt,omitempty"`
}

func (s *Service) ingestStatus(w http.ResponseWriter, r *http.Request) error {
	var p struct {
		SourceID     string          `json:"sourceId"`
		DeviceID     string          `json:"deviceId"`
		TaskID       string          `json:"taskId"`
		Status       string          `json:"status"`
		Checkpoint   json.RawMessage `json:"checkpoint"`
		Coverage     json.RawMessage `json:"coverage,omitempty"`
		LastEventAt  time.Time       `json:"lastEventAt,omitempty"`
		ErrorCode    string          `json:"errorCode,omitempty"`
		Observations []observation   `json:"observations,omitempty"`
	}
	if e := decode(w, r, &p); e != nil {
		return e
	}
	if !contains([]string{"connected", "healthy", "streaming", "backpressure", "blocked", "disconnected", "error", "idle", "unconfigured", "unsupported"}, p.Status) || len(p.ErrorCode) > 128 {
		return fail(400, "invalid_source_status")
	}
	// Serialize observations against a concurrent desired-state change.
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	var source Source
	err := s.db.Update(func(tx *bolt.Tx) error {
		c, e := ingestionCredential(tx, r, "ingest-status")
		if e != nil {
			return e
		}
		source, e = scopedSource(tx, c, p.SourceID, p.TaskID, p.DeviceID)
		if e != nil {
			return e
		}
		if len(p.Observations) > 0 && !contains(c.Capabilities, "observe-model") {
			return fail(403, "observation_scope_required")
		}
		source.Status = p.Status
		if p.Checkpoint != nil {
			source.Checkpoint = p.Checkpoint
		}
		if p.Coverage != nil {
			source.Coverage = p.Coverage
		}
		source.ErrorCode = p.ErrorCode
		source.LastSeenAt = s.opts.Now().UTC()
		if !p.LastEventAt.IsZero() {
			if p.LastEventAt.After(s.opts.Now().Add(5 * time.Minute)) {
				return fail(400, "invalid_event_time")
			}
			source.LastEventAt = p.LastEventAt
		}
		for _, o := range p.Observations {
			if err := s.applyObservation(tx, source, o); err != nil {
				return err
			}
		}
		return put(tx, "sources", source.ID, source)
	})
	if err == nil {
		respond(w, 200, source)
	}
	return err
}

func (s *Service) applyObservation(tx *bolt.Tx, source Source, o observation) error {
	task, err := read[Task](tx, "tasks", source.TaskID)
	if err != nil {
		return err
	}
	if o.Revision != 0 && o.Revision != task.Revision {
		return fail(409, "stale_observation")
	}
	index := -1
	for i, b := range task.Models {
		if b.Slot == o.Slot {
			index = i
			break
		}
	}
	if index < 0 {
		return fail(409, "observation_target_mismatch")
	}
	binding := &task.Models[index]
	if o.PackageID != "" && o.PackageID != binding.PackageID {
		return fail(409, "observation_target_mismatch")
	}
	if o.State != "loaded" && o.State != "failed" {
		return fail(400, "invalid_observation")
	}
	if o.State == "loaded" {
		model, err := read[ModelPackage](tx, "models", binding.PackageID)
		if err != nil || !model.Immutable || o.ModelID == "" || o.ModelID != model.ModelID || !digest.MatchString(o.ModelHash) || o.ModelHash != model.ModelHash {
			return fail(409, "observed_model_mismatch")
		}
		if o.ObservedAt.IsZero() || o.ObservedAt.After(s.opts.Now().Add(5*time.Minute)) {
			return fail(400, "invalid_observation_time")
		}
	}
	var op Operation
	if o.OperationID != "" {
		op, err = read[Operation](tx, "operations", o.OperationID)
		if err != nil {
			return fail(409, "observation_target_mismatch")
		}
	} else {
		rows, err := list[Operation](tx, "operations")
		if err != nil {
			return err
		}
		for _, candidate := range rows {
			if candidate.TaskID == task.ID && candidate.Slot == o.Slot && candidate.PackageID == binding.PackageID && candidate.Revision == task.Revision && (candidate.Status == "awaiting_observation" || candidate.Status == "succeeded" || (candidate.Status == "failed" && candidate.ErrorCode == "model_observation_timeout")) {
				op = candidate
				break
			}
		}
	}
	if op.ID != "" {
		if op.TaskID != task.ID || op.DeviceID != source.DeviceID || op.Slot != o.Slot || op.PackageID != binding.PackageID || op.Revision != task.Revision {
			return fail(409, "observation_target_mismatch")
		}
		if op.Status != "awaiting_observation" && op.Status != "succeeded" && !(op.Status == "failed" && op.ErrorCode == "model_observation_timeout") {
			return fail(409, "operation_not_observable")
		}
		if o.State == "loaded" && o.ObservedAt.Before(op.CreatedAt) {
			return fail(409, "observation_precedes_deployment")
		}
		if o.State == "loaded" {
			op.Status = "succeeded"
			op.ErrorCode = ""
		} else {
			op.Status = "failed"
			op.ErrorCode = "runtime_model_load_failed"
		}
		op.UpdatedAt = s.opts.Now().UTC()
		if err := put(tx, "operations", op.ID, op); err != nil {
			return err
		}
	} else if o.State == "failed" {
		return fail(409, "operation_required_for_failure")
	}
	binding.State = o.State
	if o.State == "loaded" {
		binding.ObservedPackageID = binding.PackageID
		binding.ObservedModelID = o.ModelID
		binding.ObservedModelHash = o.ModelHash
		binding.ObservedAt = o.ObservedAt.UTC()
	}
	return put(tx, "tasks", task.ID, task)
}
