package edgestack

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/gorilla/mux"
	bolt "go.etcd.io/bbolt"
)

var deliveryBuckets = []string{"delivery-requests", "delivery-jobs", "delivery-recipes", "delivery-policies", "delivery-artifacts", "delivery-reports", "delivery-plans", "delivery-rollouts", "delivery-outbox", "delivery-observers", "delivery-execution-grants", "delivery-execution-leases"}

const deliveryEpochFile = "delivery-control-epoch"
const deliveryLease = 120 * time.Second

type deliverySystemPrincipal struct{}

func deliveryDigest(v any) string {
	// Sorting map keys recursively gives both Go and Python workers one canonical
	// JSON representation. Whole-valued floats are normalized by the worker.
	raw, _ := json.Marshal(v)
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	delete(object, "digest")
	raw, _ = json.Marshal(object)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func deliveryHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func deliveryImage(value string) bool {
	ref, err := reference.ParseNormalizedNamed(value)
	if err != nil {
		return false
	}
	_, ok := ref.(reference.Canonical)
	return ok
}
func (s *Service) deliveryEnabled() bool {
	return s.opts.DeliveryEnabled || os.Getenv("EDGESTACK_DELIVERY_ENABLED") == "true"
}
func (s *Service) initDelivery() error {
	if err := s.initDeliveryObserverKey(); err != nil {
		return err
	}
	epoch := newID()
	file := filepath.Join(filepath.Dir(s.db.Path()), deliveryEpochFile)
	temp := file + "." + epoch
	handle, err := os.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = handle.Write([]byte(epoch)); err == nil {
		err = handle.Sync()
	}
	closeErr := handle.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, file); err != nil {
		_ = os.Remove(temp)
		return err
	}
	directory, err := os.Open(filepath.Dir(file))
	if err != nil {
		return err
	}
	err = directory.Sync()
	_ = directory.Close()
	if err != nil {
		return err
	}
	s.deliveryEpoch = epoch
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, name := range deliveryBuckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		jobs, err := list[deliveryJobRecord](tx, "delivery-jobs")
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if job.Status == "claimed" {
				job.Status = "needs_review"
				job.Phase = "needs_review"
				job.ErrorCode = "worker_reconciliation_required"
				job.SecretHash = ""
				if request, e := read[ModelRequest](tx, "delivery-requests", job.RequestID); e == nil {
					request.Status = "needs_review"
					request.ErrorCode = job.ErrorCode
					request.UpdatedAt = s.opts.Now().UTC()
					if e = put(tx, "delivery-requests", request.ID, request); e != nil {
						return e
					}
				}
				if err := put(tx, "delivery-jobs", job.ID, job); err != nil {
					return err
				}
			}
		}
		rollouts, err := list[DeliveryRollout](tx, "delivery-rollouts")
		if err != nil {
			return err
		}
		for _, rollout := range rollouts {
			if rollout.Status == "dispatching" || rollout.Status == "running" || rollout.Status == "rolling_back" {
				rollout.Status = "needs_review"
				rollout.ErrorCode = "delivery_restart_reconciliation_required"
				if err = put(tx, "delivery-rollouts", rollout.ID, rollout); err != nil {
					return err
				}
			}
		}
		for _, recipe := range []DeliveryRecipe{{ID: "asr-rknn-v1", Version: "1", Engine: "rknn", TaskKind: "voice-test", Architecture: "arm64", RuntimeBackend: "rknn", Slot: "asr-encoder"}, {ID: "yolo-rknn-v1", Version: "1", Engine: "rknn", TaskKind: "vision", Architecture: "arm64", RuntimeBackend: "rknn", Slot: "vision"}} {
			if _, err := read[DeliveryRecipe](tx, "delivery-recipes", recipe.ID); errors.Is(err, errNotFound) {
				recipe.MetadataVersion = 1
				if err := put(tx, "delivery-recipes", recipe.ID, recipe); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
func (s *Service) deliveryWorker(r *http.Request) error {
	if !s.deliveryEnabled() {
		return fail(503, "delivery_disabled")
	}
	path := s.opts.DeliveryWorkerTokenFile
	if path == "" {
		path = os.Getenv("EDGESTACK_DELIVERY_WORKER_TOKEN_FILE")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fail(503, "delivery_worker_unconfigured")
	}
	token, err := os.ReadFile(path)
	if err != nil {
		return fail(503, "delivery_worker_unconfigured")
	}
	expected := strings.TrimSpace(string(token))
	actual := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(expected) < 32 || subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
		return fail(401, "delivery_worker_unauthorized")
	}
	current, err := os.ReadFile(filepath.Join(filepath.Dir(s.db.Path()), deliveryEpochFile))
	if err != nil || string(current) != s.deliveryEpoch {
		return fail(409, "delivery_control_epoch_changed")
	}
	return nil
}
func (s *Service) deliveryRequestAccess(tx *bolt.Tx, r *http.Request, item ModelRequest) error {
	p, err := s.principal(r)
	if err != nil {
		return err
	}
	if !p.Admin && p.UserID != item.ActorUserID {
		return fail(404, "not_found")
	}
	if _, err = s.authorizedDevice(r, tx, item.DeviceID); err != nil {
		return fail(404, "not_found")
	}
	if item.TaskID != "" {
		if _, err = s.authorizedTask(r, tx, item.TaskID); err != nil {
			return fail(404, "not_found")
		}
	}
	return nil
}
func (s *Service) deliveryRequests(w http.ResponseWriter, r *http.Request) error {
	p, err := s.principal(r)
	if err != nil {
		return err
	}
	if r.Method == http.MethodGet {
		var result = []ModelRequest{}
		err = s.db.View(func(tx *bolt.Tx) error {
			rows, e := list[ModelRequest](tx, "delivery-requests")
			if e != nil {
				return e
			}
			for _, row := range rows {
				if s.deliveryRequestAccess(tx, r, row) == nil {
					result = append(result, row)
				}
			}
			return nil
		})
		if err == nil {
			respond(w, 200, map[string]any{"items": result, "enabled": s.deliveryEnabled()})
		}
		return err
	}
	var body struct {
		DeviceID  string `json:"deviceId"`
		TaskID    string `json:"taskId"`
		ModelName string `json:"modelName"`
		Message   string `json:"message"`
	}
	if err = decode(w, r, &body); err != nil {
		return err
	}
	if !validID(body.DeviceID) || strings.TrimSpace(body.ModelName) == "" || len(body.ModelName) > 200 || len(body.Message) > 8000 {
		return fail(400, "invalid_model_request")
	}
	now := s.opts.Now().UTC()
	item := ModelRequest{ID: newID(), ActorUserID: p.UserID, DeviceID: body.DeviceID, TaskID: body.TaskID, ModelName: body.ModelName, Status: "requested", Messages: []DeliveryMessage{}, CreatedAt: now, UpdatedAt: now}
	if body.Message != "" {
		item.Messages = append(item.Messages, DeliveryMessage{ID: newID(), ActorUserID: p.UserID, Message: body.Message, CreatedAt: now})
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		if e := s.deliveryRequestAccess(tx, r, item); e != nil {
			return e
		}
		if e := put(tx, "delivery-requests", item.ID, item); e != nil {
			return e
		}
		return s.auditTx(tx, r, "delivery.request", "model-requests", item.ID, []string{"deviceId", "taskId", "modelName"})
	})
	if err == nil {
		respond(w, 201, item)
	}
	return err
}
func (s *Service) deliveryRequest(w http.ResponseWriter, r *http.Request) error {
	var item ModelRequest
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		item, e = read[ModelRequest](tx, "delivery-requests", mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		return s.deliveryRequestAccess(tx, r, item)
	})
	if err == nil {
		respond(w, 200, item)
	}
	return err
}
func (s *Service) deliveryMessage(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Message string `json:"message"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Message) == "" || len(body.Message) > 8000 {
		return fail(400, "invalid_message")
	}
	principal, err := s.principal(r)
	if err != nil {
		return err
	}
	var item ModelRequest
	err = s.db.Update(func(tx *bolt.Tx) error {
		var e error
		item, e = read[ModelRequest](tx, "delivery-requests", mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		if e = s.deliveryRequestAccess(tx, r, item); e != nil {
			return e
		}
		if len(item.Messages) >= 100 {
			return fail(409, "request_message_limit")
		}
		item.Messages = append(item.Messages, DeliveryMessage{ID: newID(), ActorUserID: principal.UserID, Message: body.Message, CreatedAt: s.opts.Now().UTC()})
		item.UpdatedAt = s.opts.Now().UTC()
		return put(tx, "delivery-requests", item.ID, item)
	})
	if err == nil {
		respond(w, 200, item)
	}
	return err
}
func validDeliveryRecipe(recipe DeliveryRecipe) bool {
	gate := recipe.Validation
	return validID(recipe.ID) && recipe.Version != "" && recipe.Engine != "" && recipe.TaskKind != "" && recipe.Architecture != "" && recipe.RuntimeBackend != "" && recipe.TemplateID != "" && recipe.Slot != "" && deliveryImage(recipe.BaseImage) && digest.MatchString(recipe.RuntimeProfileDigest) && len(recipe.SourcePrefixes) > 0 && validID(recipe.ValidationTaskID) && validID(recipe.ValidationSourceID) && gate.MaxLatencyMs > 0 && gate.MaxMemoryBytes > 0 && gate.MaxErrorRate >= 0 && gate.MaxErrorRate <= 1 && gate.MinQuality >= 0 && gate.MinQuality <= 1
}
func deliverySourceAllowed(recipe DeliveryRecipe, source DeliverySource) bool {
	if !digest.MatchString(source.SHA256) || source.ModelID == "" || source.Format == "" {
		return false
	}
	parsed, err := url.Parse(source.URI)
	if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return false
	}
	for _, prefix := range recipe.SourcePrefixes {
		if prefix != "" && strings.HasPrefix(source.URI, prefix) {
			return true
		}
	}
	return false
}
func (s *Service) deliveryRecipes(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	if r.Method == http.MethodGet {
		var rows []DeliveryRecipe
		err := s.db.View(func(tx *bolt.Tx) error { var e error; rows, e = list[DeliveryRecipe](tx, "delivery-recipes"); return e })
		if err == nil {
			respond(w, 200, map[string]any{"items": rows})
		}
		return err
	}
	var item DeliveryRecipe
	if err := decode(w, r, &item); err != nil {
		return err
	}
	if item.ID != mux.Vars(r)["id"] || !validID(item.ID) || (item.Enabled && !validDeliveryRecipe(item)) {
		return fail(400, "invalid_delivery_recipe")
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		old, e := read[DeliveryRecipe](tx, "delivery-recipes", item.ID)
		if e != nil && !errors.Is(e, errNotFound) {
			return e
		}
		if item.MetadataVersion != old.MetadataVersion {
			return fail(409, "metadata_version_conflict")
		}
		item.MetadataVersion++
		if e = put(tx, "delivery-recipes", item.ID, item); e != nil {
			return e
		}
		return s.auditTx(tx, r, "delivery.recipe", "delivery-recipes", item.ID, []string{"configuration"})
	})
	if err == nil {
		respond(w, 200, item)
	}
	return err
}
func (s *Service) deliveryPolicies(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	if r.Method == http.MethodGet {
		var rows []DeliveryPolicy
		err := s.db.View(func(tx *bolt.Tx) error {
			var e error
			rows, e = list[DeliveryPolicy](tx, "delivery-policies")
			return e
		})
		if err == nil {
			respond(w, 200, map[string]any{"items": rows})
		}
		return err
	}
	var item DeliveryPolicy
	if err := decode(w, r, &item); err != nil {
		return err
	}
	if item.ID != mux.Vars(r)["id"] || !validID(item.ID) || (item.Enabled && (len(item.RecipeIDs) == 0 || len(item.DeviceIDs) == 0)) {
		return fail(400, "invalid_delivery_policy")
	}
	if err := s.validateBusinessACL(item.ACL); err != nil {
		return err
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		old, e := read[DeliveryPolicy](tx, "delivery-policies", item.ID)
		if e != nil && !errors.Is(e, errNotFound) {
			return e
		}
		if item.MetadataVersion != old.MetadataVersion {
			return fail(409, "metadata_version_conflict")
		}
		item.MetadataVersion++
		if e = put(tx, "delivery-policies", item.ID, item); e != nil {
			return e
		}
		return s.auditTx(tx, r, "delivery.policy", "delivery-policies", item.ID, []string{"configuration"})
	})
	if err == nil {
		respond(w, 200, item)
	}
	return err
}
func deliveryPolicyAllows(policy DeliveryPolicy, item ModelRequest, recipe DeliveryRecipe, principal Principal) bool {
	return policy.Enabled && contains(policy.RecipeIDs, recipe.ID) && contains(policy.DeviceIDs, item.DeviceID) && allows(principal, policy.ACL)
}
func (s *Service) deliveryResolve(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	if !s.deliveryEnabled() {
		return fail(503, "delivery_disabled")
	}
	var body struct {
		RecipeID string         `json:"recipeId"`
		PolicyID string         `json:"policyId"`
		Source   DeliverySource `json:"source"`
	}
	retry := strings.HasSuffix(r.URL.Path, "/retry")
	if err := decode(w, r, &body); err != nil {
		return err
	}
	var result DeliveryJob
	resolver, _ := s.principal(r)
	err := s.db.Update(func(tx *bolt.Tx) error {
		item, e := read[ModelRequest](tx, "delivery-requests", mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		if e = s.deliveryRequestAccess(tx, r, item); e != nil {
			return e
		}
		var previous *deliveryJobRecord
		if item.JobID != "" {
			prior, e := read[deliveryJobRecord](tx, "delivery-jobs", item.JobID)
			if e != nil {
				return e
			}
			if !retry {
				return fail(409, "request_already_resolved")
			}
			if prior.Status != "failed" && prior.Status != "needs_review" {
				return fail(409, "job_not_retryable")
			}
			if prior.ValidationOperationID != "" {
				if operation, e := read[Operation](tx, "operations", prior.ValidationOperationID); e == nil && (operation.Status == "dispatching" || operation.Status == "awaiting_observation") {
					return fail(409, "delivery_reconciliation_required")
				}
			}
			previous = &prior
			body.RecipeID = prior.RecipeID
			body.PolicyID = prior.PolicyID
			body.Source = prior.Source
		} else if retry {
			return fail(409, "request_not_resolved")
		}
		recipe, e := read[DeliveryRecipe](tx, "delivery-recipes", body.RecipeID)
		if e != nil || !recipe.Enabled || !validDeliveryRecipe(recipe) || !deliverySourceAllowed(recipe, body.Source) {
			return fail(409, "delivery_recipe_unavailable")
		}
		policy, e := read[DeliveryPolicy](tx, "delivery-policies", body.PolicyID)
		if e != nil {
			return fail(409, "delivery_policy_unavailable")
		}
		owner := Principal{UserID: item.ActorUserID}
		if s.opts.InspectAccess == nil {
			return fail(503, "delivery_authorization_unavailable")
		}
		device, e := read[Device](tx, "devices", item.DeviceID)
		if e != nil {
			return e
		}
		if device.Architecture != recipe.Architecture {
			return fail(409, "delivery_architecture_mismatch")
		}
		owner, allowed, e := s.opts.InspectAccess(owner.UserID, device.EndpointID)
		if e != nil || !allowed || !allows(owner, device.ACL) || !deliveryPolicyAllows(policy, item, recipe, owner) {
			return fail(403, "delivery_policy_denied")
		}
		if item.TaskID != "" {
			task, e := read[Task](tx, "tasks", item.TaskID)
			if e != nil || task.DeviceID != device.ID || !allows(owner, task.ACL) || task.Archived {
				return fail(404, "not_found")
			}
		}
		test, e := read[Task](tx, "tasks", recipe.ValidationTaskID)
		if e != nil || !test.TestTask || test.Kind != recipe.TaskKind || test.RuntimeBackend != recipe.RuntimeBackend || test.TemplateID != recipe.TemplateID {
			return fail(409, "validation_task_unavailable")
		}
		testDevice, e := read[Device](tx, "devices", test.DeviceID)
		if e != nil || testDevice.Architecture != recipe.Architecture || testDevice.Archived {
			return fail(409, "validation_task_unavailable")
		}
		now := s.opts.Now().UTC()
		result = DeliveryJob{ID: newID(), RequestID: item.ID, Kind: "prepare_model", Status: "queued", RecipeID: recipe.ID, Recipe: recipe, RecipeDigest: deliveryDigest(recipe), Source: body.Source, DeviceID: item.DeviceID, ValidationTaskID: test.ID, ValidationDeviceID: test.DeviceID, PolicyID: policy.ID, PolicyVersion: policy.MetadataVersion, Policy: policy, ActorUserID: item.ActorUserID, ResolverUserID: resolver.UserID, CreatedAt: now, UpdatedAt: now, ControlEpoch: s.deliveryEpoch}
		if previous != nil && previous.RecipeDigest == result.RecipeDigest && previous.PolicyVersion == result.PolicyVersion && previous.Source == result.Source {
			result = previous.DeliveryJob
			result.Status = "queued"
			result.Phase = "queued"
			result.ErrorCode = ""
			result.WorkerID = ""
			result.ControlEpoch = s.deliveryEpoch
			result.ClaimEpoch++
			result.LeaseExpiresAt = time.Time{}
			result.UpdatedAt = now
			if result.ValidationOperationID != "" {
				if operation, e := read[Operation](tx, "operations", result.ValidationOperationID); e == nil && operation.Status != "succeeded" {
					result.ValidationOperationID = ""
				}
			}
		}
		if e = put(tx, "delivery-jobs", result.ID, deliveryJobRecord{DeliveryJob: result}); e != nil {
			return e
		}
		item.JobID = result.ID
		item.RecipeID = recipe.ID
		item.Status = "queued"
		item.ErrorCode = ""
		item.UpdatedAt = now
		if e = put(tx, "delivery-requests", item.ID, item); e != nil {
			return e
		}
		if e = put(tx, "delivery-outbox", result.ID, map[string]any{"kind": "queued", "jobId": result.ID}); e != nil {
			return e
		}
		return s.auditTx(tx, r, "delivery.resolved", "model-requests", item.ID, []string{"recipeId", "policyId", "sourceDigest"})
	})
	if err == nil {
		respond(w, 202, result)
	}
	return err
}

// RunDelivery expires abandoned worker leases without replaying a physical step.
// A fresh claim requires an explicit reconciliation/retry of an abandoned job.
func (s *Service) RunDelivery(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closed:
			return
		case <-ticker.C:
			if s.opts.BackgroundWriteLease != nil {
				unlock, err := s.opts.BackgroundWriteLease()
				if err != nil {
					continue
				}
				_ = s.expireDelivery()
				_ = s.advanceDeliveryRollouts(ctx)
				unlock()
			} else {
				_ = s.expireDelivery()
				_ = s.advanceDeliveryRollouts(ctx)
			}
		}
	}
}
func (s *Service) expireDelivery() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		jobs, err := list[deliveryJobRecord](tx, "delivery-jobs")
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if job.Status == "claimed" && !s.opts.Now().Before(job.LeaseExpiresAt) {
				job.Status = "needs_review"
				job.Phase = "needs_review"
				job.ErrorCode = "worker_lease_expired"
				job.SecretHash = ""
				job.UpdatedAt = s.opts.Now().UTC()
				if request, e := read[ModelRequest](tx, "delivery-requests", job.RequestID); e == nil {
					request.Status = "needs_review"
					request.ErrorCode = job.ErrorCode
					request.UpdatedAt = job.UpdatedAt
					if e = put(tx, "delivery-requests", request.ID, request); e != nil {
						return e
					}
				}
				if err = put(tx, "delivery-jobs", job.ID, job); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
