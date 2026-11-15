package edgestack

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"time"

	bolt "go.etcd.io/bbolt"
)

var errPreviewRollback = errors.New("preview transaction rollback")

func (s *Service) importChanges(tx *bolt.Tx, config BootstrapConfig) ([]ImportChange, error) {
	out := []ImportChange{}
	add := func(kind, id string, fields []string) {
		action := "update"
		v := s.metadataVersion(tx, kind, id)
		if v == 0 {
			action = "create"
		} else if len(fields) == 0 {
			action = "unchanged"
		}
		out = append(out, ImportChange{Kind: kind, ID: id, Action: action, Fields: fields, MetadataVersion: v})
	}
	for _, item := range config.Devices {
		old, e := read[Device](tx, "devices", item.ID)
		if e != nil {
			add("devices", item.ID, []string{"registration"})
			continue
		}
		if item.EndpointID != old.EndpointID {
			return nil, fail(409, "device_target_immutable")
		}
		candidate := old
		candidate.Name = item.Name
		candidate.Description = item.Description
		candidate.Archived = item.Archived
		candidate.ACL = item.ACL
		add("devices", item.ID, changedFields(old, candidate))
	}
	for _, item := range config.Tasks {
		old, e := read[Task](tx, "tasks", item.ID)
		if e != nil {
			add("tasks", item.ID, []string{"registration"})
			continue
		}
		if item.DeviceID != old.DeviceID || item.Kind != old.Kind || item.Target.EndpointID != old.Target.EndpointID || item.Target.StackID != old.Target.StackID || item.Target.ServiceID != old.Target.ServiceID {
			return nil, fail(409, "task_target_immutable")
		}
		candidate := old
		candidate.Name = item.Name
		candidate.Description = item.Description
		candidate.Archived = item.Archived
		candidate.ACL = item.ACL
		candidate.DataLocations = item.DataLocations
		candidate.Capabilities = item.Capabilities
		candidate.ModelEnvKeys = item.ModelEnvKeys
		candidate.VoiceOrigin = item.VoiceOrigin
		candidate.VoiceTokenFile = item.VoiceTokenFile
		candidate.TestTask = item.TestTask
		candidate.RuntimeBackend = item.RuntimeBackend
		candidate.TemplateID = item.TemplateID
		add("tasks", item.ID, changedFields(old, candidate))
	}
	for _, item := range config.Sources {
		old, e := read[Source](tx, "sources", item.ID)
		if e != nil {
			add("sources", item.ID, []string{"registration"})
			continue
		}
		if item.DeviceID != old.DeviceID || item.TaskID != old.TaskID || item.Type != old.Type {
			return nil, fail(409, "source_target_immutable")
		}
		candidate := old
		candidate.Name = item.Name
		candidate.Description = item.Description
		candidate.Enabled = item.Enabled
		candidate.Archived = item.Archived
		add("sources", item.ID, changedFields(old, candidate))
	}
	for _, item := range config.Models {
		old, e := read[ModelPackage](tx, "models", item.ID)
		if e != nil {
			add("models", item.ID, []string{"immutablePackage"})
			continue
		}
		candidate := item
		candidate.Published = old.Published
		if !reflect.DeepEqual(old, candidate) {
			return nil, fail(409, "package_immutable")
		}
		add("models", item.ID, nil)
	}
	for _, item := range config.Credentials {
		old, e := read[credentialRecord](tx, "credentials", item.ID)
		if e != nil {
			add("credentials", item.ID, []string{"scope"})
			continue
		}
		if old.Hash != item.TokenHash || !reflect.DeepEqual(old.Credential.DeviceIDs, item.DeviceIDs) || !reflect.DeepEqual(old.Credential.TaskIDs, item.TaskIDs) || !reflect.DeepEqual(old.Credential.SourceIDs, item.SourceIDs) || !reflect.DeepEqual(old.Credential.Capabilities, item.Capabilities) {
			return nil, fail(409, "credential_rotation_required")
		}
		add("credentials", item.ID, nil)
	}
	return out, nil
}
func (s *Service) applyImport(ctx context.Context, tx *bolt.Tx, config BootstrapConfig, changes []ImportChange) error {
	for _, change := range changes {
		if s.metadataVersion(tx, change.Kind, change.ID) != change.MetadataVersion {
			return fail(409, "metadata_version_conflict")
		}
	}
	if err := s.seedTx(ctx, tx, config); err != nil {
		return err
	}
	for _, change := range changes {
		if change.Action != "update" {
			continue
		}
		p := metadataPatch{MetadataVersion: change.MetadataVersion}
		switch change.Kind {
		case "devices":
			for _, item := range config.Devices {
				if item.ID == change.ID {
					p.Name = &item.Name
					p.Description = &item.Description
					p.Archived = &item.Archived
					p.UserIDs = &item.UserIDs
					p.TeamIDs = &item.TeamIDs
					break
				}
			}
		case "tasks":
			for _, item := range config.Tasks {
				if item.ID == change.ID {
					p.Name = &item.Name
					p.Description = &item.Description
					p.Archived = &item.Archived
					p.UserIDs = &item.UserIDs
					p.TeamIDs = &item.TeamIDs
					p.DataLocations = &item.DataLocations
					old, _ := read[Task](tx, "tasks", item.ID)
					if !reflect.DeepEqual(item.Capabilities, old.Capabilities) {
						p.Capabilities = &item.Capabilities
					}
					if !reflect.DeepEqual(item.ModelEnvKeys, old.ModelEnvKeys) {
						p.ModelEnvKeys = &item.ModelEnvKeys
					}
					if item.VoiceOrigin != old.VoiceOrigin {
						p.VoiceOrigin = &item.VoiceOrigin
					}
					if item.VoiceTokenFile != old.VoiceTokenFile {
						p.VoiceTokenFile = &item.VoiceTokenFile
					}
					if item.TestTask != old.TestTask {
						p.TestTask = &item.TestTask
					}
					if item.RuntimeBackend != old.RuntimeBackend {
						p.RuntimeBackend = &item.RuntimeBackend
					}
					if item.TemplateID != old.TemplateID {
						p.TemplateID = &item.TemplateID
					}
					break
				}
			}
		case "sources":
			for _, item := range config.Sources {
				if item.ID == change.ID {
					p.Name = &item.Name
					p.Description = &item.Description
					p.Enabled = &item.Enabled
					p.Archived = &item.Archived
					break
				}
			}
		default:
			return fail(409, "immutable_import_object")
		}
		if _, _, err := s.applyPatch(tx, change.Kind, change.ID, p); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) importPreview(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	var p struct {
		Config BootstrapConfig `json:"config"`
	}
	if err := decode(w, r, &p); err != nil {
		return err
	}
	principal, err := s.principal(r)
	if err != nil {
		return err
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	var changes []ImportChange
	err = s.db.Update(func(tx *bolt.Tx) error {
		var e error
		changes, e = s.importChanges(tx, p.Config)
		if e != nil {
			return e
		}
		if e = s.applyImport(r.Context(), tx, p.Config, changes); e != nil {
			return e
		}
		return errPreviewRollback
	})
	if !errors.Is(err, errPreviewRollback) {
		return err
	}
	plan := importRecord{ID: newID(), UserID: principal.UserID, ExpiresAt: s.opts.Now().Add(5 * time.Minute).UTC(), Config: p.Config, Changes: changes}
	err = s.db.Update(func(tx *bolt.Tx) error { return put(tx, "imports", plan.ID, plan) })
	if err == nil {
		respond(w, 200, map[string]any{"id": plan.ID, "expiresAt": plan.ExpiresAt, "changes": plan.Changes})
	}
	return err
}
func (s *Service) importConfirm(w http.ResponseWriter, r *http.Request) error {
	if err := s.admin(r); err != nil {
		return err
	}
	var p struct {
		PreviewID string `json:"previewId"`
		Confirm   bool   `json:"confirm"`
	}
	if err := decode(w, r, &p); err != nil {
		return err
	}
	if !p.Confirm {
		return fail(400, "confirmation_required")
	}
	principal, err := s.principal(r)
	if err != nil {
		return err
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	var plan importRecord
	err = s.db.Update(func(tx *bolt.Tx) error {
		var e error
		plan, e = read[importRecord](tx, "imports", p.PreviewID)
		if e != nil {
			return e
		}
		if plan.UserID != principal.UserID {
			return errNotFound
		}
		if plan.Applied {
			return nil
		}
		if s.opts.Now().After(plan.ExpiresAt) {
			return fail(409, "preview_expired")
		}
		if e = s.applyImport(r.Context(), tx, plan.Config, plan.Changes); e != nil {
			return e
		}
		for _, change := range plan.Changes {
			if change.Action != "unchanged" {
				if e = s.auditTx(tx, r, "config.import", change.Kind, change.ID, change.Fields); e != nil {
					return e
				}
			}
		}
		plan.Applied = true
		return put(tx, "imports", plan.ID, plan)
	})
	if err == nil {
		respond(w, 200, map[string]any{"applied": true, "changes": plan.Changes})
	}
	return err
}
