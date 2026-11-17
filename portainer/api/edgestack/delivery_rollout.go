package edgestack

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	bolt "go.etcd.io/bbolt"
)

func (s *Service) deliveryPlanCreate(w http.ResponseWriter, r *http.Request) error {
	principal, err := s.principal(r)
	if err != nil {
		return err
	}
	var body struct {
		DeliveryTarget
		Targets   []DeliveryTarget `json:"targets"`
		PackageID string           `json:"packageId"`
		RecipeID  string           `json:"recipeId"`
	}
	if err = decode(w, r, &body); err != nil {
		return err
	}
	if len(body.Targets) == 0 {
		body.Targets = []DeliveryTarget{body.DeliveryTarget}
	}
	if len(body.Targets) > 16 {
		return fail(400, "too_many_deployment_targets")
	}
	plan := deliveryPlan{ID: newID(), ActorUserID: principal.UserID, PackageID: body.PackageID, Status: "ready", ExpiresAt: s.opts.Now().Add(5 * time.Minute).UTC(), Targets: []DeliveryPlanTarget{}}
	seen := map[string]bool{}
	for _, target := range body.Targets {
		key := target.DeviceID + ":" + target.TaskID
		if seen[key] {
			return fail(400, "duplicate_deployment_target")
		}
		seen[key] = true
		var device Device
		var model ModelPackage
		var task Task
		var recipe DeliveryRecipe
		err = s.db.View(func(tx *bolt.Tx) error {
			var e error
			device, e = s.authorizedDevice(r, tx, target.DeviceID)
			if e != nil {
				return e
			}
			model, e = read[ModelPackage](tx, "models", body.PackageID)
			if e != nil || !model.Published {
				return fail(404, "not_found")
			}
			if target.TaskID != "" {
				task, e = s.authorizedTask(r, tx, target.TaskID)
				if e != nil || task.DeviceID != device.ID {
					return fail(404, "not_found")
				}
				if !compatible(task, device, model) || !s.policyAllows(tx, task, device, model, principal) {
					return fail(409, "package_incompatible")
				}
				return nil
			}
			if s.opts.DeliveryProvisioner == nil {
				return fail(503, "new_task_provider_unconfigured")
			}
			recipeID := body.RecipeID
			if model.ArtifactID != "" {
				artifact, e := read[DeliveryArtifact](tx, "delivery-artifacts", model.ArtifactID)
				if e != nil {
					return e
				}
				job, e := read[deliveryJobRecord](tx, "delivery-jobs", artifact.JobID)
				if e != nil {
					return e
				}
				recipeID = job.RecipeID
			}
			recipe, e = read[DeliveryRecipe](tx, "delivery-recipes", recipeID)
			if e != nil || !recipe.Enabled {
				return fail(409, "delivery_recipe_unavailable")
			}
			task = Task{ID: "delivery-task-" + newID(), DeviceID: device.ID, Name: target.TaskName, Kind: recipe.TaskKind, RuntimeBackend: recipe.RuntimeBackend, TemplateID: recipe.TemplateID, Models: []ModelBinding{{Slot: model.Slot}}, Capabilities: []string{"replace-model"}, ACL: ACL{UserIDs: []int{principal.UserID}}}
			for key := range model.Environment {
				task.ModelEnvKeys = append(task.ModelEnvKeys, key)
			}
			if !compatible(task, device, model) || !s.policyAllows(tx, task, device, model, principal) {
				return fail(409, "package_incompatible")
			}
			return nil
		})
		if err != nil {
			return err
		}
		entry := DeliveryPlanTarget{DeliveryTarget: target, NewTask: target.TaskID == ""}
		if entry.NewTask {
			entry.TaskID = task.ID
			name := target.TaskName
			if name == "" {
				name = model.Name
			}
			if len(name) > 200 {
				return fail(400, "invalid_task_name")
			}
			entry.TaskName = name
			preview, e := s.opts.DeliveryProvisioner.Preview(r.Context(), DeliveryProvisionSpec{Device: device, Package: model, Recipe: recipe, TaskID: task.ID, Name: name, ACL: task.ACL})
			if e != nil {
				return fail(409, "target_not_provisionable")
			}
			entry.Provision = &preview
			plan.Recipe = recipe
		} else {
			preview, e := s.deliveryPreview(r, task, model, false)
			if e != nil {
				return e
			}
			entry.Preview = preview
		}
		plan.Targets = append(plan.Targets, entry)
	}
	if len(plan.Targets) == 1 {
		first := plan.Targets[0]
		plan.DeviceID = first.DeviceID
		plan.TaskID = first.TaskID
		plan.Preview = first.Preview
		plan.Provision = first.Provision
	}
	err = s.db.Update(func(tx *bolt.Tx) error { return put(tx, "delivery-plans", plan.ID, plan) })
	if err == nil {
		respond(w, 200, plan)
	}
	return err
}
func (s *Service) deliveryPlanConfirm(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if !body.Confirm {
		return fail(400, "confirmation_required")
	}
	principal, err := s.principal(r)
	if err != nil {
		return err
	}
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	var plan deliveryPlan
	var rollout DeliveryRollout
	err = s.db.Update(func(tx *bolt.Tx) error {
		var e error
		plan, e = read[deliveryPlan](tx, "delivery-plans", mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		if plan.ActorUserID != principal.UserID {
			return fail(404, "not_found")
		}
		for _, target := range plan.Targets {
			if _, e = s.authorizedDevice(r, tx, target.DeviceID); e != nil {
				return e
			}
			if !target.NewTask {
				if _, e = s.authorizedTask(r, tx, target.TaskID); e != nil {
					return e
				}
			}
		}
		if plan.OperationID != "" {
			rollout, e = read[DeliveryRollout](tx, "delivery-rollouts", plan.OperationID)
			return e
		}
		if plan.Status != "ready" || !s.opts.Now().Before(plan.ExpiresAt) {
			return fail(409, "deployment_plan_expired")
		}
		if s.opts.DeliveryObserveHealth == nil {
			return fail(503, "delivery_health_observer_unconfigured")
		}
		rollout = DeliveryRollout{ID: newID(), PlanID: plan.ID, ActorUserID: principal.UserID, PackageID: plan.PackageID, Status: "dispatching", UpdatedAt: s.opts.Now().UTC(), Targets: []DeliveryRolloutTarget{}}
		for _, target := range plan.Targets {
			rollout.Targets = append(rollout.Targets, DeliveryRolloutTarget{DeliveryPlanTarget: target, Status: "pending"})
		}
		plan.OperationID = rollout.ID
		plan.Status = "confirmed"
		if e = put(tx, "delivery-plans", plan.ID, plan); e != nil {
			return e
		}
		return put(tx, "delivery-rollouts", rollout.ID, rollout)
	})
	if err != nil {
		return err
	}
	if rollout.Status == "dispatching" {
		err = s.dispatchDeliveryTarget(r, &rollout, plan)
		if err != nil {
			return err
		}
	}
	respond(w, 202, rollout)
	return nil
}
func (s *Service) dispatchDeliveryTarget(r *http.Request, rollout *DeliveryRollout, plan deliveryPlan) error {
	index := rollout.CurrentTarget
	entry := &rollout.Targets[index]
	if entry.Status != "pending" {
		return nil
	}
	// Persist an intent before invoking the physical provider. Startup never
	// automatically replays a dispatch whose result could have been lost.
	entry.Status = "dispatching"
	entry.OperationID = "delivery-op-" + newID()
	if err := s.saveDeliveryRollout(*rollout); err != nil {
		return err
	}
	var operation Operation
	var err error
	if !entry.NewTask {
		// Refresh subsequent target previews after the canary's health window.
		var task Task
		var model ModelPackage
		err = s.db.View(func(tx *bolt.Tx) error {
			var e error
			task, e = s.authorizedTask(r, tx, entry.TaskID)
			if e != nil {
				return e
			}
			model, e = read[ModelPackage](tx, "models", plan.PackageID)
			return e
		})
		preview := entry.Preview
		if err == nil && index > 0 {
			preview, err = s.deliveryPreview(r, task, model, false)
		}
		if err == nil {
			operation, err = s.deliveryChange(r, task.ID, preview.ID, false)
		}
		if err == nil {
			entry.PreviousPackageID = operation.PreviousPackageID
			entry.OperationID = operation.ID
		}
	} else {
		var spec DeliveryProvisionSpec
		spec, err = s.deliveryProvisionSpec(r, plan, *entry, rollout.ActorUserID)
		if err == nil {
			// Persist the task and a hashed, exactly scoped observer credential
			// before the provider installs its immutable service template.
			err = s.db.Update(func(tx *bolt.Tx) error {
				task := Task{ID: spec.TaskID, DeviceID: spec.Device.ID, Name: spec.Name, Kind: spec.Recipe.TaskKind,
					RuntimeBackend: spec.Recipe.RuntimeBackend, TemplateID: spec.Recipe.TemplateID,
					State: "provisioning", MetadataVersion: 1, Revision: 1, ACL: spec.ACL,
					Target: Target{EndpointID: spec.Device.EndpointID}, Models: []ModelBinding{{Slot: spec.Package.Slot, PackageID: spec.Package.ID, State: "awaiting_observation"}}}
				if _, e := read[Task](tx, "tasks", task.ID); e == nil {
					return fail(409, "task_identity_conflict")
				}
				if e := put(tx, "tasks", task.ID, task); e != nil {
					return e
				}
				source := Source{ID: "delivery-source-" + newID(), DeviceID: task.DeviceID, TaskID: task.ID, Name: spec.Name + " model observer", Type: "model.observer", Enabled: true, MetadataVersion: 1, Status: "pending"}
				if e := put(tx, "sources", source.ID, source); e != nil {
					return e
				}
				credential, _, e := s.createCredentialRecord(tx, r, Credential{Name: "delivery observer", DeviceIDs: []string{task.DeviceID}, TaskIDs: []string{task.ID}, SourceIDs: []string{source.ID}, Capabilities: []string{"ingest-status", "observe-model"}}, "")
				if e != nil {
					return e
				}
				binding := deliveryObserverBinding{OperationID: entry.OperationID, TaskID: task.ID, SourceID: source.ID, CredentialID: credential.ID}
				token := s.deliveryObserverToken(binding)
				if e = put(tx, "credentials", credential.ID, credentialRecord{credential, deliveryHash(token)}); e != nil {
					return e
				}
				if e = put(tx, "delivery-observers", task.ID, binding); e != nil {
					return e
				}
				spec.ObserverSourceID = source.ID
				spec.ObserverToken = token
				return e
			})
			if err != nil {
				entry.Status = "needs_review"
				rollout.Status = "needs_review"
				_ = s.saveDeliveryRollout(*rollout)
				return err
			}
			var task Task
			task, err = s.opts.DeliveryProvisioner.Apply(r.Context(), spec, *entry.Provision, entry.OperationID)
			if err == nil {
				if task.ID != spec.TaskID || task.DeviceID != spec.Device.ID || task.Kind != spec.Recipe.TaskKind || task.RuntimeBackend != spec.Recipe.RuntimeBackend || task.TemplateID != spec.Recipe.TemplateID || task.Target.EndpointID != spec.Device.EndpointID || task.Target.StackID <= 0 || task.Target.ServiceID == "" {
					err = errors.New("provisioned task identity mismatch")
				} else {
					task.ACL = spec.ACL
					task.Revision = 1
					task.MetadataVersion = 1
					task.Models = []ModelBinding{{Slot: spec.Package.Slot, PackageID: spec.Package.ID, Name: spec.Package.Name, Version: spec.Package.Version, State: "awaiting_observation"}}
					operation = Operation{ID: entry.OperationID, TaskID: task.ID, DeviceID: task.DeviceID, ActorUserID: rollout.ActorUserID, PackageID: spec.Package.ID, Slot: spec.Package.Slot, Revision: task.Revision, Target: task.Target, Status: "awaiting_observation", CreatedAt: s.opts.Now().UTC(), UpdatedAt: s.opts.Now().UTC()}
					err = s.db.Update(func(tx *bolt.Tx) error {
						prior, e := read[Task](tx, "tasks", task.ID)
						if e != nil || prior.State != "provisioning" {
							return fail(409, "task_identity_conflict")
						}
						if e := put(tx, "tasks", task.ID, task); e != nil {
							return e
						}
						return put(tx, "operations", operation.ID, operation)
					})
				}
			}
		}
	}
	if err != nil {
		entry.Status = "dispatch_unconfirmed"
		_ = s.beginDeliveryRollback(rollout, "delivery_dispatch_unconfirmed")
		return err
	}
	entry.Status = "awaiting_observation"
	rollout.Status = "running"
	rollout.OperationID = entry.OperationID
	rollout.DeviceID = entry.DeviceID
	rollout.TaskID = entry.TaskID
	return s.saveDeliveryRollout(*rollout)
}
func (s *Service) deliveryProvisionSpec(r *http.Request, plan deliveryPlan, entry DeliveryRolloutTarget, actor int) (DeliveryProvisionSpec, error) {
	var spec DeliveryProvisionSpec
	err := s.db.View(func(tx *bolt.Tx) error {
		device, e := s.authorizedDevice(r, tx, entry.DeviceID)
		if e != nil {
			return e
		}
		model, e := read[ModelPackage](tx, "models", plan.PackageID)
		if e != nil {
			return e
		}
		if !model.Published {
			return fail(409, "package_withdrawn")
		}
		recipe, e := read[DeliveryRecipe](tx, "delivery-recipes", plan.Recipe.ID)
		if e != nil || !recipe.Enabled || deliveryDigest(recipe) != deliveryDigest(plan.Recipe) {
			return fail(409, "delivery_recipe_changed")
		}
		principal, e := s.principal(r)
		if e != nil {
			return e
		}
		candidate := Task{Kind: recipe.TaskKind, RuntimeBackend: recipe.RuntimeBackend, TemplateID: recipe.TemplateID, Capabilities: []string{"replace-model"}, Models: []ModelBinding{{Slot: recipe.Slot}}}
		for key := range model.Environment {
			candidate.ModelEnvKeys = append(candidate.ModelEnvKeys, key)
		}
		if !compatible(candidate, device, model) || !s.policyAllows(tx, candidate, device, model, principal) {
			return fail(403, "delivery_policy_denied")
		}
		spec = DeliveryProvisionSpec{Device: device, Package: model, Recipe: recipe, TaskID: entry.TaskID, Name: entry.TaskName, ACL: ACL{UserIDs: []int{actor}}}
		if binding, e := read[deliveryObserverBinding](tx, "delivery-observers", entry.TaskID); e == nil {
			spec.ObserverSourceID = binding.SourceID
			spec.ObserverToken = s.deliveryObserverToken(binding)
		}
		return nil
	})
	return spec, err
}
func (s *Service) saveDeliveryRollout(item DeliveryRollout) error {
	item.UpdatedAt = s.opts.Now().UTC()
	return s.db.Update(func(tx *bolt.Tx) error { return put(tx, "delivery-rollouts", item.ID, item) })
}
func (s *Service) deliveryRollouts(w http.ResponseWriter, r *http.Request) error {
	principal, err := s.principal(r)
	if err != nil {
		return err
	}
	var result = []DeliveryRollout{}
	err = s.db.View(func(tx *bolt.Tx) error {
		rows, e := list[DeliveryRollout](tx, "delivery-rollouts")
		if e != nil {
			return e
		}
		for _, row := range rows {
			if !principal.Admin && row.ActorUserID != principal.UserID {
				continue
			}
			allowed := true
			for _, entry := range row.Targets {
				if _, e = s.authorizedDevice(r, tx, entry.DeviceID); e != nil {
					allowed = false
				}
			}
			if allowed && (mux.Vars(r)["id"] == "" || row.ID == mux.Vars(r)["id"]) {
				for i := range row.Targets {
					if row.Targets[i].OperationID != "" {
						if op, e := read[Operation](tx, "operations", row.Targets[i].OperationID); e == nil && row.Targets[i].Status == "awaiting_observation" && op.Status != "awaiting_observation" {
							row.Targets[i].Status = op.Status
						}
					}
				}
				result = append(result, row)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if mux.Vars(r)["id"] != "" {
		if len(result) == 0 {
			return fail(404, "not_found")
		}
		respond(w, 200, result[0])
	} else {
		respond(w, 200, map[string]any{"items": result})
	}
	return nil
}
func (s *Service) advanceDeliveryRollouts(ctx context.Context) error {
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	var rows []DeliveryRollout
	if err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		rows, e = list[DeliveryRollout](tx, "delivery-rollouts")
		return e
	}); err != nil {
		return err
	}
	for _, item := range rows {
		if item.Status != "running" && item.Status != "rolling_back" {
			continue
		}
		var savedPlan deliveryPlan
		if item.RollingBack {
			_ = s.db.View(func(tx *bolt.Tx) error {
				var e error
				savedPlan, e = read[deliveryPlan](tx, "delivery-plans", item.PlanID)
				return e
			})
			current := &item.Targets[item.CurrentTarget]
			if current.Status != "rollback_observing" {
				req, _ := http.NewRequestWithContext(ctx, "POST", "/api/edgestack/deployment-plans", nil)
				var endpoint int
				_ = s.db.View(func(tx *bolt.Tx) error {
					device, e := read[Device](tx, "devices", current.DeviceID)
					endpoint = device.EndpointID
					return e
				})
				req = s.deliveryInternalRequest(req, item.ActorUserID, endpoint)
				if err := s.restoreDeliveryTarget(ctx, req, &item, savedPlan); err != nil {
					item.Status = "needs_review"
					item.ErrorCode = "delivery_rollback_unconfirmed"
					_ = s.saveDeliveryRollout(item)
					continue
				}
				if current.Status == "rolled_back" || current.Status == "pending" {
					if item.CurrentTarget == 0 {
						item.Status = "rolled_back"
					} else {
						item.CurrentTarget--
					}
					_ = s.saveDeliveryRollout(item)
					continue
				}
			}
		}
		var plan deliveryPlan
		var task Task
		var model ModelPackage
		var operation Operation
		if err := s.db.View(func(tx *bolt.Tx) error {
			var e error
			plan, e = read[deliveryPlan](tx, "delivery-plans", item.PlanID)
			if e != nil {
				return e
			}
			entry := item.Targets[item.CurrentTarget]
			task, e = read[Task](tx, "tasks", entry.TaskID)
			if e != nil {
				return e
			}
			modelID := item.PackageID
			if item.RollingBack && entry.PreviousPackageID != "" {
				modelID = entry.PreviousPackageID
			}
			model, e = read[ModelPackage](tx, "models", modelID)
			if e != nil {
				return e
			}
			operation, e = read[Operation](tx, "operations", entry.OperationID)
			return e
		}); err != nil {
			continue
		}
		request, _ := http.NewRequestWithContext(ctx, "POST", "/api/edgestack/deployment-plans", nil)
		request = s.deliveryInternalRequest(request, item.ActorUserID, task.Target.EndpointID)
		entry := &item.Targets[item.CurrentTarget]
		if err := s.db.View(func(tx *bolt.Tx) error { _, e := s.authorizedTask(request, tx, entry.TaskID); return e }); err != nil {
			item.Status = "needs_review"
			item.ErrorCode = "delivery_authorization_changed"
			_ = s.saveDeliveryRollout(item)
			continue
		}
		if operation.Status == "failed" || operation.Status == "needs_review" {
			entry.Status = operation.Status
			if item.RollingBack {
				item.Status = "needs_review"
				item.ErrorCode = "delivery_rollback_failed"
				_ = s.saveDeliveryRollout(item)
			} else {
				_ = s.beginDeliveryRollback(&item, operation.ErrorCode)
			}
			continue
		}
		if operation.Status != "succeeded" {
			continue
		}
		if entry.ObservingAt.IsZero() {
			entry.ObservingAt = s.opts.Now().UTC()
		}
		if s.opts.DeliveryObserveHealth == nil {
			item.Status = "needs_review"
			item.ErrorCode = "delivery_health_observer_unconfigured"
			_ = s.saveDeliveryRollout(item)
			continue
		}
		if entry.LastHealthAt.IsZero() || s.opts.Now().Sub(entry.LastHealthAt) >= 10*time.Second {
			if err := s.opts.DeliveryObserveHealth(ctx, task, model); err != nil {
				entry.Status = "failed"
				if item.RollingBack {
					item.Status = "needs_review"
					item.ErrorCode = "delivery_rollback_health_failed"
					_ = s.saveDeliveryRollout(item)
				} else {
					_ = s.beginDeliveryRollback(&item, "delivery_health_failed")
				}
				continue
			}
			entry.HealthChecks++
			entry.LastHealthAt = s.opts.Now().UTC()
		}
		if entry.HealthChecks >= 3 && s.opts.Now().Sub(entry.ObservingAt) >= 120*time.Second {
			if item.RollingBack {
				entry.Status = "rolled_back"
				if item.CurrentTarget == 0 {
					item.Status = "rolled_back"
				} else {
					item.CurrentTarget--
				}
				_ = s.saveDeliveryRollout(item)
				continue
			}
			entry.Status = "succeeded"
			item.CurrentTarget++
			if item.CurrentTarget == len(item.Targets) {
				item.Status = "succeeded"
			} else {
				if err := s.dispatchDeliveryTarget(request, &item, plan); err != nil {
					_ = s.beginDeliveryRollback(&item, "delivery_dispatch_unconfirmed")
				}
			}
		}
		_ = s.saveDeliveryRollout(item)
	}
	return nil
}
