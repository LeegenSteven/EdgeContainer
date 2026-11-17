package edgestack

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	bolt "go.etcd.io/bbolt"
)

func (s *Service) claimDelivery(tx *bolt.Tx, id string, claim DeliveryClaim) (deliveryJobRecord, error) {
	job, err := read[deliveryJobRecord](tx, "delivery-jobs", id)
	if err != nil {
		return job, err
	}
	if job.Status != "claimed" || job.WorkerID != claim.WorkerID || job.ClaimEpoch != claim.ClaimEpoch || job.ControlEpoch != claim.ControlEpoch || claim.ControlEpoch != s.deliveryEpoch || !s.opts.Now().Before(job.LeaseExpiresAt) || job.SecretHash == "" || deliveryHash(claim.ClaimToken) != job.SecretHash {
		return job, fail(409, "delivery_claim_lost")
	}
	return job, nil
}
func (s *Service) deliveryClaim(w http.ResponseWriter, r *http.Request) error {
	if err := s.deliveryWorker(r); err != nil {
		return err
	}
	var body struct {
		WorkerID  string   `json:"workerId"`
		RecipeIDs []string `json:"recipeIds"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if !validID(body.WorkerID) || len(body.RecipeIDs) == 0 {
		return fail(400, "invalid_worker_claim")
	}
	var result *DeliveryJob
	token := newID() + newID()
	err := s.db.Update(func(tx *bolt.Tx) error {
		jobs, e := list[deliveryJobRecord](tx, "delivery-jobs")
		if e != nil {
			return e
		}
		for _, job := range jobs {
			if job.Status != "queued" || !contains(body.RecipeIDs, job.RecipeID) {
				continue
			}
			// Current policy/recipe and owner ACL are rechecked before handing out work.
			if e = s.deliveryJobAuthorized(tx, r, job); e != nil {
				job.Status = "needs_review"
				job.ErrorCode = "delivery_authorization_changed"
				if e = put(tx, "delivery-jobs", job.ID, job); e != nil {
					return e
				}
				continue
			}
			busy := false
			for _, other := range jobs {
				if other.ID != job.ID && other.Status == "claimed" && other.ValidationTaskID == job.ValidationTaskID {
					busy = true
				}
			}
			if busy {
				continue
			}
			now := s.opts.Now().UTC()
			job.Status = "claimed"
			job.Phase = "waiting_for_device"
			job.WorkerID = body.WorkerID
			job.ClaimEpoch++
			job.Attempt++
			job.ControlEpoch = s.deliveryEpoch
			job.LeaseExpiresAt = now.Add(deliveryLease)
			job.UpdatedAt = now
			job.SecretHash = deliveryHash(token)
			if e = put(tx, "delivery-jobs", job.ID, job); e != nil {
				return e
			}
			if e = tx.Bucket([]byte("delivery-outbox")).Delete([]byte(job.ID)); e != nil {
				return e
			}
			result = &job.DeliveryJob
			return nil
		}
		return nil
	})
	if err == nil {
		respond(w, 200, map[string]any{"job": result, "claimToken": token, "controlEpoch": s.deliveryEpoch, "heartbeatSeconds": 30, "claimEpoch": func() int {
			if result == nil {
				return 0
			}
			return result.ClaimEpoch
		}(), "leaseExpiresAt": func() time.Time {
			if result == nil {
				return time.Time{}
			}
			return result.LeaseExpiresAt
		}()})
	}
	return err
}
func (s *Service) deliveryJobAuthorized(tx *bolt.Tx, r *http.Request, job deliveryJobRecord) error {
	current, err := read[DeliveryRecipe](tx, "delivery-recipes", job.RecipeID)
	if err != nil || !current.Enabled || deliveryDigest(current) != job.RecipeDigest {
		return fail(409, "delivery_recipe_changed")
	}
	policy, err := read[DeliveryPolicy](tx, "delivery-policies", job.PolicyID)
	if err != nil || !policy.Enabled || policy.MetadataVersion != job.PolicyVersion {
		return fail(409, "delivery_policy_changed")
	}
	item, err := read[ModelRequest](tx, "delivery-requests", job.RequestID)
	if err != nil {
		return err
	}
	device, err := read[Device](tx, "devices", item.DeviceID)
	if err != nil || device.Archived {
		return fail(409, "delivery_target_changed")
	}
	if s.opts.InspectAccess == nil {
		return fail(503, "delivery_authorization_unavailable")
	}
	owner, allowed, err := s.opts.InspectAccess(item.ActorUserID, device.EndpointID)
	if err != nil || !allowed || !allows(owner, device.ACL) || !deliveryPolicyAllows(policy, item, current, owner) {
		return fail(403, "delivery_policy_denied")
	}
	if item.TaskID != "" {
		task, err := read[Task](tx, "tasks", item.TaskID)
		if err != nil || task.Archived || task.DeviceID != device.ID || !allows(owner, task.ACL) {
			return fail(403, "delivery_policy_denied")
		}
	}
	return nil
}
func (s *Service) deliveryHeartbeat(w http.ResponseWriter, r *http.Request) error {
	if err := s.deliveryWorker(r); err != nil {
		return err
	}
	var body struct {
		DeliveryClaim
		Step  string `json:"step,omitempty"`
		Phase string `json:"phase,omitempty"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	var expires time.Time
	err := s.db.Update(func(tx *bolt.Tx) error {
		job, e := s.claimDelivery(tx, mux.Vars(r)["id"], body.DeliveryClaim)
		if e != nil {
			return e
		}
		if e = s.deliveryJobAuthorized(tx, r, job); e != nil {
			return e
		}
		phase := body.Phase
		if phase == "" && body.Step != "" {
			phase = map[string]string{"native_plan": "waiting_for_device", "native_run": "native_validation", "build": "building", "registry": "building", "offline": "policy_evaluation", "validation": "image_validation"}[body.Step]
		}
		if phase != "" {
			if !contains([]string{"waiting_for_device", "native_validation", "building", "image_validation", "policy_evaluation"}, phase) {
				return fail(400, "invalid_delivery_phase")
			}
			job.Phase = phase
		}
		job.LeaseExpiresAt = s.opts.Now().Add(deliveryLease).UTC()
		job.UpdatedAt = s.opts.Now().UTC()
		expires = job.LeaseExpiresAt
		return put(tx, "delivery-jobs", job.ID, job)
	})
	if err == nil {
		respond(w, 200, map[string]any{"leaseExpiresAt": expires, "controlEpoch": s.deliveryEpoch})
	}
	return err
}
func (s *Service) readDeliveryClaim(r *http.Request, id string, claim DeliveryClaim) (deliveryJobRecord, error) {
	var job deliveryJobRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		job, e = s.claimDelivery(tx, id, claim)
		if e != nil {
			return e
		}
		return s.deliveryJobAuthorized(tx, r, job)
	})
	return job, err
}
func (s *Service) deliveryValidationStart(w http.ResponseWriter, r *http.Request) error {
	if err := s.deliveryWorker(r); err != nil {
		return err
	}
	var body struct {
		DeliveryClaim
		Package ModelPackage `json:"package"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	job, err := s.readDeliveryClaim(r, mux.Vars(r)["id"], body.DeliveryClaim)
	if err != nil {
		return err
	}
	if job.ValidationOperationID != "" {
		return s.deliveryValidationStatusBody(w, r, job)
	}
	model := body.Package
	recipe := job.Recipe
	model.ArtifactID = ""
	if model.ID != "delivery-"+job.ID || model.Published || model.ModelID != job.Source.ModelID || model.ModelHash != job.Source.SHA256 || model.RuntimeBackend != recipe.RuntimeBackend || model.Slot != recipe.Slot || len(model.TaskKinds) != 1 || model.TaskKinds[0] != recipe.TaskKind || len(model.Architectures) != 1 || model.Architectures[0] != recipe.Architecture || len(model.TemplateIDs) != 1 || model.TemplateIDs[0] != recipe.TemplateID {
		return fail(400, "delivery_package_mismatch")
	}
	a, _ := json.Marshal(model.Environment)
	b, _ := json.Marshal(recipe.ModelEnvironment)
	if !bytes.Equal(a, b) {
		return fail(400, "delivery_environment_mismatch")
	}
	if err = validateModelBody(model); err != nil {
		return err
	}
	var task Task
	err = s.db.Update(func(tx *bolt.Tx) error {
		if _, e := s.claimDelivery(tx, job.ID, body.DeliveryClaim); e != nil {
			return e
		}
		var e error
		task, e = read[Task](tx, "tasks", recipe.ValidationTaskID)
		if e != nil {
			return e
		}
		source, e := read[Source](tx, "sources", recipe.ValidationSourceID)
		if e != nil || !source.Enabled || source.TaskID != task.ID || source.Type != "model.observer" || !task.TestTask {
			return fail(409, "validation_task_unavailable")
		}
		if old, e := read[ModelPackage](tx, "models", model.ID); e == nil {
			left, _ := json.Marshal(old)
			right, _ := json.Marshal(model)
			if !bytes.Equal(left, right) {
				return fail(409, "immutable_package_conflict")
			}
		} else if e != errNotFound {
			return e
		} else {
			if e = put(tx, "models", model.ID, model); e != nil {
				return e
			}
			if e = put(tx, "model-policies", model.ID, ModelPolicy{MetadataVersion: 1, ValidationStatus: "draft", ValidatedScopes: []PublicationScope{}}); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	internal := s.deliveryInternalRequest(r, job.ResolverUserID, task.Target.EndpointID)
	preview, err := s.deliveryPreview(internal, task, model, true)
	if err != nil {
		return err
	}
	op, err := s.deliveryChange(internal, task.ID, preview.ID, true)
	if err != nil {
		return err
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		current, e := s.claimDelivery(tx, job.ID, body.DeliveryClaim)
		if e != nil {
			return e
		}
		current.ValidationOperationID = op.ID
		current.ValidationPackageID = model.ID
		return put(tx, "delivery-jobs", job.ID, current)
	})
	if err == nil {
		respond(w, 200, map[string]any{"operationId": op.ID, "taskId": task.ID, "status": op.Status})
	}
	return err
}
func (s *Service) deliveryValidationStatusBody(w http.ResponseWriter, r *http.Request, job deliveryJobRecord) error {
	if job.ValidationOperationID == "" {
		return fail(409, "delivery_validation_not_started")
	}
	var operation Operation
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		operation, e = read[Operation](tx, "operations", job.ValidationOperationID)
		return e
	})
	if err == nil {
		respond(w, 200, map[string]any{"operationId": operation.ID, "taskId": operation.TaskID, "status": operation.Status, "errorCode": operation.ErrorCode})
	}
	return err
}
func (s *Service) deliveryValidationStatus(w http.ResponseWriter, r *http.Request) error {
	if err := s.deliveryWorker(r); err != nil {
		return err
	}
	var claim DeliveryClaim
	if err := decode(w, r, &claim); err != nil {
		return err
	}
	job, err := s.readDeliveryClaim(r, mux.Vars(r)["id"], claim)
	if err != nil {
		return err
	}
	return s.deliveryValidationStatusBody(w, r, job)
}
func deliveryReportValid(job deliveryJobRecord, model ModelPackage, report DeliveryReport, now time.Time) bool {
	gate := job.Recipe.Validation
	return validID(report.ID) && report.JobID == job.ID && report.RecipeDigest == job.RecipeDigest && digest.MatchString(report.ReleaseDigest) && report.Image == model.Image && report.ModelHash == model.ModelHash && report.RuntimeProfileDigest == job.Recipe.RuntimeProfileDigest && report.TaskID == job.ValidationTaskID && report.Status == "passed" && report.NativeVerified && report.ContainerVerified && !report.Simulated && !report.ObservedAt.IsZero() && !report.ObservedAt.After(now.Add(time.Minute)) && !report.ObservedAt.Before(job.CreatedAt) && !math.IsNaN(report.LatencyMs) && !math.IsInf(report.LatencyMs, 0) && report.LatencyMs >= 0 && report.LatencyMs <= gate.MaxLatencyMs && report.MemoryBytes > 0 && report.MemoryBytes <= gate.MaxMemoryBytes && report.ErrorRate >= 0 && report.ErrorRate <= gate.MaxErrorRate && report.Quality >= gate.MinQuality && report.Quality <= 1
}
func (s *Service) deliveryValidationComplete(w http.ResponseWriter, r *http.Request) error {
	if err := s.deliveryWorker(r); err != nil {
		return err
	}
	var body struct {
		DeliveryClaim
		Report DeliveryReport `json:"report"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	job, err := s.readDeliveryClaim(r, mux.Vars(r)["id"], body.DeliveryClaim)
	if err != nil {
		return err
	}
	var task Task
	var model ModelPackage
	var op Operation
	var source Source
	err = s.db.View(func(tx *bolt.Tx) error {
		var e error
		task, e = read[Task](tx, "tasks", job.ValidationTaskID)
		if e != nil {
			return e
		}
		model, e = read[ModelPackage](tx, "models", job.ValidationPackageID)
		if e != nil {
			return e
		}
		op, e = read[Operation](tx, "operations", job.ValidationOperationID)
		if e != nil {
			return e
		}
		source, e = read[Source](tx, "sources", job.Recipe.ValidationSourceID)
		return e
	})
	if err != nil {
		return err
	}
	report := body.Report
	if report.DeviceID != task.DeviceID || !deliveryReportValid(job, model, report, s.opts.Now()) {
		return fail(409, "delivery_validation_report_mismatch")
	}
	report.Digest = deliveryDigest(report)
	if body.Report.Digest != "" && body.Report.Digest != report.Digest {
		return fail(409, "report_digest_mismatch")
	}
	var savedReport DeliveryReport
	_ = s.db.View(func(tx *bolt.Tx) error {
		savedReport, _ = read[DeliveryReport](tx, "delivery-reports", report.ID)
		return nil
	})
	if savedReport.ID != "" {
		if savedReport.Digest != report.Digest {
			return fail(409, "immutable_report_conflict")
		}
		respond(w, 200, map[string]any{"status": "passed", "report": savedReport})
		return nil
	}
	if s.opts.VerifyModelRuntime == nil {
		return fail(503, "model_runtime_unverified")
	}
	if err = s.opts.VerifyModelRuntime(r.Context(), task, model); err != nil {
		return fail(503, "model_runtime_unverified")
	}
	if s.opts.DeliveryObserveHealth == nil {
		return fail(503, "delivery_health_observer_unconfigured")
	}
	if err = s.opts.DeliveryObserveHealth(r.Context(), task, model); err != nil {
		return fail(503, "delivery_validation_health_failed")
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		if _, e := s.claimDelivery(tx, job.ID, body.DeliveryClaim); e != nil {
			return e
		}
		if e := s.deliveryJobAuthorized(tx, r, job); e != nil {
			return e
		}
		proof := validationRuntimeProof{TaskID: task.ID, TaskRevision: task.Revision, Target: task.Target, PackageID: model.ID, Image: model.Image, ModelID: model.ModelID, ModelHash: model.ModelHash, OperationStatus: op.Status, OperationUpdatedAt: op.UpdatedAt}
		proofs := map[string]validationRuntimeProof{op.ID: proof}
		if e := checkValidationRuntimeProofs(tx, source, proofs); e != nil {
			return e
		}
		if e := s.applyObservation(tx, source, observation{OperationID: op.ID, Slot: model.Slot, PackageID: model.ID, Revision: task.Revision, State: "loaded", ModelID: model.ModelID, ModelHash: model.ModelHash, ObservedAt: report.ObservedAt}, proofs); e != nil {
			return e
		}
		if old, e := read[DeliveryReport](tx, "delivery-reports", report.ID); e == nil && old.Digest != report.Digest {
			return fail(409, "immutable_report_conflict")
		}
		return put(tx, "delivery-reports", report.ID, report)
	})
	if err == nil {
		respond(w, 200, map[string]any{"status": "passed", "report": report})
	}
	return err
}
func (s *Service) deliveryResult(w http.ResponseWriter, r *http.Request) error {
	if err := s.deliveryWorker(r); err != nil {
		return err
	}
	var body struct {
		DeliveryClaim
		ResultID  string           `json:"resultId"`
		Status    string           `json:"status"`
		ErrorCode string           `json:"errorCode,omitempty"`
		Artifact  DeliveryArtifact `json:"artifact"`
		Report    DeliveryReport   `json:"report"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if !validID(body.ResultID) || (body.Status != "failed" && body.Status != "artifact_ready") {
		return fail(400, "invalid_delivery_result")
	}
	fingerprint := deliveryDigest(body)
	var prior deliveryJobRecord
	if err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		prior, e = read[deliveryJobRecord](tx, "delivery-jobs", mux.Vars(r)["id"])
		return e
	}); err != nil {
		return err
	}
	if prior.ResultID != "" {
		if prior.WorkerID != body.WorkerID || prior.ControlEpoch != body.ControlEpoch || prior.ClaimEpoch != body.ClaimEpoch || prior.SecretHash != deliveryHash(body.ClaimToken) || prior.ResultID != body.ResultID || prior.ResultDigest != fingerprint {
			return fail(409, "delivery_result_conflict")
		}
		respond(w, 200, prior.DeliveryJob)
		return nil
	}
	if _, err := s.readDeliveryClaim(r, prior.ID, body.DeliveryClaim); err != nil {
		return err
	}
	if body.Status == "artifact_ready" {
		file, e := s.openDeliveryBundle(body.Artifact)
		if e != nil {
			return e
		}
		e = s.verifyDeliveryBundle(file, body.Artifact)
		_ = file.Close()
		if e != nil {
			return e
		}
	}
	var result DeliveryJob
	err := s.db.Update(func(tx *bolt.Tx) error {
		saved, e := read[deliveryJobRecord](tx, "delivery-jobs", mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		if saved.ResultID != "" {
			if saved.WorkerID != body.WorkerID || saved.ControlEpoch != body.ControlEpoch || saved.ClaimEpoch != body.ClaimEpoch || saved.SecretHash != deliveryHash(body.ClaimToken) || saved.ResultID != body.ResultID || saved.ResultDigest != fingerprint {
				return fail(409, "delivery_result_conflict")
			}
			result = saved.DeliveryJob
			return nil
		}
		job, e := s.claimDelivery(tx, saved.ID, body.DeliveryClaim)
		if e != nil {
			return e
		}
		if e = s.deliveryJobAuthorized(tx, r, job); e != nil {
			return e
		}
		job.ResultID = body.ResultID
		job.ResultDigest = fingerprint
		job.UpdatedAt = s.opts.Now().UTC()
		item, e := read[ModelRequest](tx, "delivery-requests", job.RequestID)
		if e != nil {
			return e
		}
		if body.Status == "failed" {
			job.Status = "failed"
			job.Phase = "failed"
			job.ErrorCode = body.ErrorCode
			if !validID(job.ErrorCode) {
				job.ErrorCode = "delivery_worker_failed"
			}
			item.Status = "failed"
			item.ErrorCode = job.ErrorCode
		} else {
			report, e := read[DeliveryReport](tx, "delivery-reports", body.Report.ID)
			if e != nil || report.JobID != job.ID || report.Digest != deliveryDigest(body.Report) {
				return fail(409, "delivery_validation_required")
			}
			artifact := body.Artifact
			model, e := read[ModelPackage](tx, "models", job.ValidationPackageID)
			if e != nil {
				return e
			}
			if !validID(artifact.ID) || artifact.JobID != job.ID || artifact.ModelID != model.ModelID || artifact.ModelHash != model.ModelHash || artifact.Image != model.Image || artifact.ReportDigest != report.Digest || artifact.RecipeDigest != job.RecipeDigest || artifact.RuntimeProfileDigest != job.Recipe.RuntimeProfileDigest || artifact.ReleaseDigest != report.ReleaseDigest || artifact.Platform != "linux/"+job.Recipe.Architecture || !artifact.RegistryVerified || !digest.MatchString(artifact.OfflineBundle.SHA256) || artifact.OfflineBundle.URI == "" || len(artifact.Files) == 0 {
				return fail(409, "delivery_artifact_mismatch")
			}
			for _, file := range artifact.Files {
				if file.Path == "" || strings.HasPrefix(file.Path, "/") || strings.Contains(file.Path, "..") || !digest.MatchString(file.SHA256) || file.SizeBytes < 0 {
					return fail(400, "invalid_artifact_file")
				}
			}
			artifact.Digest = deliveryDigest(artifact)
			if body.Artifact.Digest != "" && body.Artifact.Digest != artifact.Digest {
				return fail(409, "artifact_digest_mismatch")
			}
			if old, e := read[DeliveryArtifact](tx, "delivery-artifacts", artifact.ID); e == nil && old.Digest != artifact.Digest {
				return fail(409, "immutable_artifact_conflict")
			}
			if e = put(tx, "delivery-artifacts", artifact.ID, artifact); e != nil {
				return e
			}
			model.ArtifactID = artifact.ID
			job.ArtifactID = artifact.ID
			job.Status = "artifact_ready"
			job.Phase = "artifact_ready"
			item.Status = "artifact_ready"
			item.PackageID = model.ID
			policy := s.modelPolicy(tx, model.ID)
			if job.Policy.AutoPublish {
				model.Published = true
				job.Status = "published"
				job.Phase = "published"
				item.Status = "published"
				policy.MetadataVersion++
			}
			if e = put(tx, "model-policies", model.ID, policy); e != nil {
				return e
			}
			if e = put(tx, "models", model.ID, model); e != nil {
				return e
			}
		}
		item.UpdatedAt = job.UpdatedAt
		if e = put(tx, "delivery-requests", item.ID, item); e != nil {
			return e
		}
		if e = put(tx, "delivery-jobs", job.ID, job); e != nil {
			return e
		}
		result = job.DeliveryJob
		return s.auditTx(tx, nil, "delivery.result", "delivery-jobs", job.ID, []string{"status", "artifactId", "resultId"})
	})
	if err == nil {
		respond(w, 200, result)
	}
	return err
}
