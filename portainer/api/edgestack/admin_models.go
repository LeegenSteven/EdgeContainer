package edgestack

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/distribution/reference"
	"github.com/gorilla/mux"
	bolt "go.etcd.io/bbolt"
)

var modelEnvironmentKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func safeModelKey(key string) bool {
	upper := strings.ToUpper(key)
	return modelEnvironmentKey.MatchString(key) && !strings.Contains(upper, "TOKEN") && !strings.Contains(upper, "SECRET") && !strings.Contains(upper, "PASSWORD") && !strings.Contains(upper, "API_KEY") && key != "PATH" && key != "LD_PRELOAD" && key != "PYTHONPATH"
}
func validateModelBody(m ModelPackage) error {
	if !validID(m.ID) || strings.TrimSpace(m.Name) == "" || m.Version == "" || m.ModelID == "" || m.Slot == "" || len(m.TaskKinds) == 0 || len(m.Architectures) == 0 || m.RuntimeBackend == "" || len(m.TemplateIDs) == 0 || !m.Immutable || !digest.MatchString(m.ModelHash) {
		return fail(400, "invalid_immutable_package")
	}
	ref, err := reference.ParseNormalizedNamed(m.Image)
	if err != nil {
		return fail(400, "invalid_image_digest")
	}
	if _, ok := ref.(reference.Canonical); !ok {
		return fail(400, "invalid_image_digest")
	}
	for key, value := range m.Environment {
		if !safeModelKey(key) || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return fail(400, "invalid_model_parameter")
		}
	}
	return nil
}
func validateTaskMetadata(task Task) error {
	if task.VoiceOrigin != "" {
		u, e := url.Parse(task.VoiceOrigin)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return fail(400, "invalid_voice_origin")
		}
	}
	for _, key := range task.ModelEnvKeys {
		if !safeModelKey(key) {
			return fail(400, "invalid_model_parameter")
		}
	}
	for _, raw := range task.DataLocations {
		var location map[string]any
		if json.Unmarshal(raw, &location) != nil {
			var legacy string
			if json.Unmarshal(raw, &legacy) != nil {
				return fail(400, "invalid_data_location")
			}
			continue
		}
		for _, field := range []string{"verification", "observedAt", "runtimeState", "runtimeObservedAt", "hostSource", "inspect", "mounts"} {
			if _, set := location[field]; set {
				return fail(400, "derived_location_field")
			}
		}
	}
	return nil
}
func validSourceType(kind string) bool {
	return strings.HasPrefix(kind, "voice.") || strings.HasPrefix(kind, "camera.") || kind == "model.observer"
}

type validationKey struct{}
type validationIntent struct{ packageID, taskID string }

func (s *Service) selectionAllowed(r *http.Request, tx *bolt.Tx, task Task, device Device, model ModelPackage) bool {
	if intent, ok := r.Context().Value(validationKey{}).(validationIntent); ok {
		p, err := s.principal(r)
		return err == nil && p.Admin && intent.packageID == model.ID && intent.taskID == task.ID && task.TestTask && task.RuntimeBackend != "" && task.TemplateID != "" && model.RuntimeBackend != "" && len(model.TemplateIDs) > 0 && compatibleBody(task, device, model)
	}
	principal, err := s.principal(r)
	return err == nil && model.Published && compatibleBody(task, device, model) && s.policyAllows(tx, task, device, model, principal)
}
func validationRequest(r *http.Request, intent validationIntent, payload any) *http.Request {
	body, _ := json.Marshal(payload)
	clone := r.Clone(context.WithValue(r.Context(), validationKey{}, intent))
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	return mux.SetURLVars(clone, map[string]string{"id": intent.taskID})
}
func (s *Service) validationPreview(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	id := mux.Vars(r)["id"]
	var p struct {
		TaskID   string `json:"taskId"`
		Slot     string `json:"slot"`
		Revision int    `json:"revision"`
	}
	if err := decode(w, r, &p); err != nil {
		return err
	}
	return s.preview(w, validationRequest(r, validationIntent{id, p.TaskID}, map[string]any{"packageId": id, "slot": p.Slot, "revision": p.Revision}))
}
func (s *Service) validationConfirm(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	id := mux.Vars(r)["id"]
	var p struct {
		PreviewID string `json:"previewId"`
		Confirm   bool   `json:"confirm"`
	}
	if err := decode(w, r, &p); err != nil {
		return err
	}
	var record previewRecord
	if err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		record, err = read[previewRecord](tx, "previews", p.PreviewID)
		if err != nil {
			return err
		}
		if record.Preview.PackageID != id || record.Preview.Purpose != "model-validation" {
			return fail(409, "validation_preview_required")
		}
		return nil
	}); err != nil {
		return err
	}
	return s.changeModel(w, validationRequest(r, validationIntent{id, record.Preview.TaskID}, map[string]any{"previewId": p.PreviewID, "confirm": p.Confirm}))
}
func (s *Service) modelValidations(w http.ResponseWriter, r *http.Request) error {
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	q := u.Query()
	q.Set("packageId", mux.Vars(r)["id"])
	clone.URL.RawQuery = q.Encode()
	return s.adminItems(w, clone, "model-validations")
}
func (s *Service) beginValidation(tx *bolt.Tx, op Operation, task Task) error {
	device, err := read[Device](tx, "devices", task.DeviceID)
	if err != nil {
		return err
	}
	v := ModelValidation{ID: op.ID, OperationID: op.ID, PackageID: op.PackageID, TaskID: op.TaskID, DeviceID: op.DeviceID, ActorUserID: op.ActorUserID, Status: "validating", CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt, Scope: scopeFor(task, device)}
	if err := put(tx, "model-validations", v.ID, v); err != nil {
		return err
	}
	policy := s.modelPolicy(tx, op.PackageID)
	policy.ValidationStatus = "validating"
	policy.LastValidationID = v.ID
	policy.MetadataVersion++
	return put(tx, "model-policies", op.PackageID, policy)
}
func (s *Service) finishValidation(tx *bolt.Tx, op Operation, source Source, observation observation) error {
	if op.ValidationID == "" {
		return nil
	}
	v, err := read[ModelValidation](tx, "model-validations", op.ValidationID)
	if err != nil {
		return err
	}
	if v.Status == "passed" {
		return nil
	}
	v.UpdatedAt = s.opts.Now().UTC()
	policy := s.modelPolicy(tx, op.PackageID)
	if op.Status == "succeeded" {
		v.Status = "passed"
		v.ObservedAt = observation.ObservedAt
		v.SourceID = source.ID
		v.ModelID = observation.ModelID
		v.ModelHash = observation.ModelHash
		if !contains(policy.ValidatedScopes, v.Scope) {
			policy.ValidatedScopes = append(policy.ValidatedScopes, v.Scope)
		}
		policy.ValidationStatus = "passed"
	} else if op.Status == "failed" {
		v.Status = "failed"
		v.ErrorCode = op.ErrorCode
		policy.ValidationStatus = "failed"
		if len(policy.ValidatedScopes) > 0 {
			policy.ValidationStatus = "passed"
		}
	} else {
		return nil
	}
	policy.MetadataVersion++
	if err := put(tx, "model-validations", v.ID, v); err != nil {
		return err
	}
	return put(tx, "model-policies", op.PackageID, policy)
}
func (s *Service) auditOperation(tx *bolt.Tx, op Operation, action string) error {
	entry := AuditEntry{ID: newID(), ActorUserID: op.ActorUserID, Action: action, ResourceType: "operations", ResourceID: op.ID, DeviceID: op.DeviceID, TaskID: op.TaskID, OccurredAt: s.opts.Now().UTC(), Result: op.Status, Fields: []string{"packageId", "previousPackageId", "revision", "status"}}
	return put(tx, "audit", entry.ID, entry)
}
