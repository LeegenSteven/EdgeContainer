package edgestack

import (
	"context"
	"errors"
	"net/http"

	bolt "go.etcd.io/bbolt"
)

func (s *Service) beginDeliveryRollback(item *DeliveryRollout, code string) error {
	item.Status = "rolling_back"
	item.RollingBack = true
	item.ErrorCode = code
	return s.saveDeliveryRollout(*item)
}
func (s *Service) restoreDeliveryTarget(ctx context.Context, r *http.Request, item *DeliveryRollout, plan deliveryPlan) error {
	entry := &item.Targets[item.CurrentTarget]
	if entry.Status == "pending" || entry.Status == "rolled_back" {
		return nil
	}
	if entry.NewTask {
		s.modelMu.Lock()
		defer s.modelMu.Unlock()
		rollback, ok := s.opts.DeliveryProvisioner.(DeliveryProvisionRollback)
		if !ok {
			return fail(503, "delivery_provision_rollback_unavailable")
		}
		spec, err := s.deliveryProvisionSpec(r, plan, *entry, item.ActorUserID)
		if err != nil {
			return err
		}
		unchanged := func(task Task) bool {
			if task.Archived || task.Revision != 1 || task.DeviceID != entry.DeviceID {
				return false
			}
			for _, binding := range task.Models {
				if binding.Slot == spec.Package.Slot {
					return binding.PackageID == item.PackageID
				}
			}
			return false
		}
		var original Task
		if err = s.db.View(func(tx *bolt.Tx) error {
			var e error
			original, e = s.authorizedTask(r, tx, entry.TaskID)
			if e != nil {
				return e
			}
			if !unchanged(original) {
				return fail(409, "delivery_rollback_model_changed")
			}
			return nil
		}); err != nil {
			return err
		}
		if err = rollback.Rollback(ctx, spec, "rollback-"+entry.OperationID); err != nil {
			return err
		}
		err = s.db.Update(func(tx *bolt.Tx) error {
			task, e := read[Task](tx, "tasks", entry.TaskID)
			if e != nil {
				return e
			}
			if !unchanged(task) || task.Target != original.Target {
				return fail(409, "delivery_rollback_target_changed")
			}
			task.Archived = true
			task.State = "stopped"
			if e = put(tx, "tasks", task.ID, task); e != nil {
				return e
			}
			credentials, e := list[credentialRecord](tx, "credentials")
			if e != nil {
				return e
			}
			for _, credential := range credentials {
				if contains(credential.Credential.TaskIDs, task.ID) {
					credential.Credential.Revoked = true
					if e = put(tx, "credentials", credential.Credential.ID, credential); e != nil {
						return e
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		entry.Status = "rolled_back"
		return s.saveDeliveryRollout(*item)
	}
	if entry.PreviousPackageID == "" {
		entry.PreviousPackageID = entry.Preview.PreviousPackageID
	}
	if entry.PreviousPackageID == "" {
		return fail(409, "delivery_rollback_target_unknown")
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	var task Task
	var model ModelPackage
	if err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		task, e = s.authorizedTask(r, tx, entry.TaskID)
		if e != nil {
			return e
		}
		model, e = read[ModelPackage](tx, "models", entry.PreviousPackageID)
		if e != nil {
			return e
		}
		if !model.Immutable || !deliveryImage(model.Image) {
			return fail(409, "delivery_rollback_image_unavailable")
		}
		if entry.Preview.Target != task.Target {
			return fail(409, "delivery_rollback_target_changed")
		}
		bound := false
		for _, binding := range task.Models {
			if binding.Slot == model.Slot {
				bound = binding.PackageID == item.PackageID || binding.PackageID == entry.PreviousPackageID
				break
			}
		}
		if !bound {
			return fail(409, "delivery_rollback_model_changed")
		}
		return nil
	}); err != nil {
		return err
	}
	if s.opts.Dispatcher == nil {
		return fail(503, "replacement_unavailable")
	}
	targetVersion, err := s.opts.Dispatcher.Preview(ctx, task, model)
	if err != nil {
		return err
	}
	op := Operation{ID: newID(), TaskID: task.ID, DeviceID: task.DeviceID, ActorUserID: item.ActorUserID, PackageID: model.ID, Slot: model.Slot, Revision: task.Revision + 1, Target: task.Target, Status: "dispatching", CreatedAt: s.opts.Now().UTC(), UpdatedAt: s.opts.Now().UTC(), Purpose: "delivery-rollback"}
	if err = s.db.Update(func(tx *bolt.Tx) error { return put(tx, "operations", op.ID, op) }); err != nil {
		return err
	}
	entry.OperationID = op.ID
	entry.Status = "rollback_dispatching"
	entry.HealthChecks = 0
	entry.ObservingAt = op.CreatedAt
	entry.LastHealthAt = op.CreatedAt
	if err = s.saveDeliveryRollout(*item); err != nil {
		return err
	}
	if err = s.opts.Dispatcher.Apply(WithDeliveryOperation(ctx, op.ID), task, model, targetVersion); err != nil {
		return err
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		current, e := read[Task](tx, "tasks", task.ID)
		if e != nil {
			return e
		}
		if current.Revision != task.Revision || current.Target != task.Target {
			return errors.New("rollback target changed")
		}
		current.Revision = op.Revision
		for i := range current.Models {
			if current.Models[i].Slot == model.Slot {
				current.Models[i].PackageID = model.ID
				current.Models[i].Name = model.Name
				current.Models[i].Version = model.Version
				current.Models[i].State = "awaiting_observation"
			}
		}
		op.Status = "awaiting_observation"
		if e = put(tx, "tasks", task.ID, current); e != nil {
			return e
		}
		return put(tx, "operations", op.ID, op)
	})
	if err != nil {
		return err
	}
	entry.Status = "rollback_observing"
	return s.saveDeliveryRollout(*item)
}
