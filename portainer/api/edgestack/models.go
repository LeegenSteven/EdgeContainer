package edgestack

import (
	"net/http"
	"time"

	"github.com/gorilla/mux"
	bolt "go.etcd.io/bbolt"
)

type previewRecord struct {
	Preview Preview `json:"preview"`
	UserID  int     `json:"userId"`
}

func (s *Service) modelPublication(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	var payload struct {
		Published *bool `json:"published"`
	}
	if err := decode(w, r, &payload); err != nil {
		return err
	}
	if payload.Published == nil {
		return fail(400, "publication_state_required")
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	id := mux.Vars(r)["id"]
	err := s.db.Update(func(tx *bolt.Tx) error {
		model, err := read[ModelPackage](tx, "models", id)
		if err != nil {
			return err
		}
		model.Published = *payload.Published
		return put(tx, "models", model.ID, model)
	})
	if err == nil {
		respond(w, 200, map[string]any{"id": id, "published": *payload.Published})
	}
	return err
}

func compatible(t Task, d Device, m ModelPackage) bool {
	if !m.Published || !m.Immutable || !contains(m.TaskKinds, t.Kind) || !contains(m.Architectures, d.Architecture) {
		return false
	}
	if !contains(t.Capabilities, "replace-model") {
		return false
	}
	found := false
	for _, b := range t.Models {
		if b.Slot == m.Slot {
			found = true
		}
	}
	if !found {
		return false
	}
	for key := range m.Environment {
		if !contains(t.ModelEnvKeys, key) {
			return false
		}
	}
	return true
}
func (s *Service) models(w http.ResponseWriter, r *http.Request) error {
	if err := s.expireOperations(); err != nil {
		return err
	}
	items := []ModelPackage{}
	revision := 0
	can := false
	reason := "task_not_configured_for_replacement"
	err := s.db.View(func(tx *bolt.Tx) error {
		task, e := s.authorizedTask(r, tx, mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		revision = task.Revision
		d, e := read[Device](tx, "devices", task.DeviceID)
		if e != nil {
			return e
		}
		rows, e := list[ModelPackage](tx, "models")
		if e != nil {
			return e
		}
		if s.opts.Dispatcher != nil && contains(task.Capabilities, "replace-model") {
			for _, m := range rows {
				if compatible(task, d, m) {
					if _, e := s.opts.Dispatcher.Preview(r.Context(), task, m); e == nil {
						items = append(items, m)
					}
				}
			}
			can = len(items) > 0
			if !can {
				reason = "no_compatible_packages_or_target_unavailable"
			} else {
				reason = ""
			}
		}
		return nil
	})
	if err == nil {
		respond(w, 200, map[string]any{"items": items, "taskRevision": revision, "canReplaceModel": can, "reason": reason})
	}
	return err
}
func (s *Service) preview(w http.ResponseWriter, r *http.Request) error {
	var p struct {
		PackageID string `json:"packageId"`
		Slot      string `json:"slot"`
		Revision  int    `json:"revision"`
	}
	if e := decode(w, r, &p); e != nil {
		return e
	}
	if s.opts.Dispatcher == nil {
		return fail(409, "replacement_unavailable")
	}
	principal, e := s.principal(r)
	if e != nil {
		return e
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	var task Task
	var model ModelPackage
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		task, e = s.authorizedTask(r, tx, mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		if task.Revision != p.Revision {
			return fail(409, "task_revision_changed")
		}
		d, e := read[Device](tx, "devices", task.DeviceID)
		if e != nil {
			return e
		}
		model, e = read[ModelPackage](tx, "models", p.PackageID)
		if e != nil || model.Slot != p.Slot || !compatible(task, d, model) {
			return fail(409, "package_incompatible")
		}
		return nil
	})
	if err != nil {
		return err
	}
	version, err := s.opts.Dispatcher.Preview(r.Context(), task, model)
	if err != nil {
		return fail(409, "target_not_replaceable")
	}
	result := Preview{ID: newID(), TaskID: task.ID, PackageID: model.ID, Slot: p.Slot, Revision: task.Revision, Target: task.Target, TargetVersion: version, RequiresRestart: true, Effects: []string{"Update only the registered service image and approved model environment values", "Restart the task; retain all volumes, ports, networks, and unrelated environment values", "Wait for a trusted runtime receipt before marking the model loaded"}, ExpiresAt: s.opts.Now().Add(5 * time.Minute).UTC()}
	for _, binding := range task.Models {
		if binding.Slot == p.Slot {
			result.PreviousPackageID = binding.PackageID
			break
		}
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		return put(tx, "previews", result.ID, previewRecord{result, principal.UserID})
	})
	if err == nil {
		respond(w, 200, result)
	}
	return err
}
func (s *Service) changeModel(w http.ResponseWriter, r *http.Request) error {
	if err := s.expireOperations(); err != nil {
		return err
	}
	var p struct {
		PreviewID string `json:"previewId"`
		Confirm   bool   `json:"confirm"`
	}
	if e := decode(w, r, &p); e != nil {
		return e
	}
	if !p.Confirm {
		return fail(400, "confirmation_required")
	}
	principal, e := s.principal(r)
	if e != nil {
		return e
	}
	if s.opts.Dispatcher == nil {
		return fail(409, "replacement_unavailable")
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	var task Task
	var model ModelPackage
	var preview Preview
	var op Operation
	duplicate := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		var e error
		task, e = s.authorizedTask(r, tx, mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		record, e := read[previewRecord](tx, "previews", p.PreviewID)
		if e != nil || record.UserID != principal.UserID || record.Preview.TaskID != task.ID {
			return fail(404, "preview_not_found")
		}
		preview = record.Preview
		if preview.OperationID != "" {
			op, e = read[Operation](tx, "operations", preview.OperationID)
			duplicate = true
			return e
		}
		if preview.ExpiresAt.Before(s.opts.Now()) {
			return fail(409, "preview_expired")
		}
		if task.Revision != preview.Revision || task.Target != preview.Target {
			return fail(409, "task_revision_changed")
		}
		d, e := read[Device](tx, "devices", task.DeviceID)
		if e != nil {
			return e
		}
		model, e = read[ModelPackage](tx, "models", preview.PackageID)
		if e != nil || !compatible(task, d, model) {
			return fail(409, "package_incompatible")
		}
		operations, e := list[Operation](tx, "operations")
		if e != nil {
			return e
		}
		for _, active := range operations {
			if active.TaskID == task.ID && (active.Status == "dispatching" || active.Status == "awaiting_observation") {
				return fail(409, "model_change_in_progress")
			}
		}
		op = Operation{ID: newID(), TaskID: task.ID, DeviceID: task.DeviceID, Status: "dispatching", PackageID: model.ID, Slot: preview.Slot, Revision: task.Revision + 1, CreatedAt: s.opts.Now().UTC(), UpdatedAt: s.opts.Now().UTC()}
		op.PreviousPackageID = preview.PreviousPackageID
		if op.PreviousPackageID == "" {
			for _, binding := range task.Models {
				if binding.Slot == preview.Slot {
					op.PreviousPackageID = binding.PackageID
					break
				}
			}
		}
		record.Preview.OperationID = op.ID
		if e := put(tx, "previews", preview.ID, record); e != nil {
			return e
		}
		return put(tx, "operations", op.ID, op)
	})
	if err != nil {
		return err
	}
	if duplicate {
		respond(w, 200, op)
		return nil
	}
	applyErr := s.opts.Dispatcher.Apply(r.Context(), task, model, preview.TargetVersion)
	err = s.db.Update(func(tx *bolt.Tx) error {
		// The dispatch was authorized before its side effect. Its outcome is
		// system bookkeeping and must be recorded even if the caller loses
		// access or its session expires while the deployment is in progress.
		current, e := read[Task](tx, "tasks", task.ID)
		if e != nil {
			return e
		}
		op.UpdatedAt = s.opts.Now().UTC()
		if applyErr != nil {
			op.Status = "failed"
			op.ErrorCode = "deployment_failed"
		} else {
			op.Status = "awaiting_observation"
			current.Revision = op.Revision
			for i := range current.Models {
				if current.Models[i].Slot == model.Slot {
					current.Models[i].PackageID = model.ID
					current.Models[i].Name = model.Name
					current.Models[i].Version = model.Version
					current.Models[i].State = "awaiting_observation"
				}
			}
			if e := put(tx, "tasks", current.ID, current); e != nil {
				return e
			}
		}
		return put(tx, "operations", op.ID, op)
	})
	if err != nil {
		return err
	}
	// Authorization to view the result is independent of the durable
	// bookkeeping. A revoked caller receives no operation or task details.
	if err := s.db.View(func(tx *bolt.Tx) error { _, err := s.authorizedTask(r, tx, task.ID); return err }); err != nil {
		return err
	}
	respond(w, 202, op)
	return nil
}
func (s *Service) operation(w http.ResponseWriter, r *http.Request) error {
	if err := s.expireOperations(); err != nil {
		return err
	}
	var op Operation
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		op, e = read[Operation](tx, "operations", mux.Vars(r)["id"])
		if e != nil {
			return e
		}
		_, e = s.authorizedTask(r, tx, op.TaskID)
		return e
	})
	if err == nil {
		respond(w, 200, op)
	}
	return err
}
