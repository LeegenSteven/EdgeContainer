package edgestack

import (
	"context"
	"encoding/json"
	"errors"
	bolt "go.etcd.io/bbolt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Startup configuration seeds absent IDs only. Database metadata is authoritative.
type BootstrapConfig struct {
	Devices     []Device              `json:"devices"`
	Tasks       []Task                `json:"tasks"`
	Sources     []Source              `json:"sources"`
	Models      []ModelPackage        `json:"models"`
	Credentials []BootstrapCredential `json:"credentials"`
}
type BootstrapCredential struct {
	Credential
	TokenHash string `json:"tokenHash"`
}

var tokenHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (s *Service) Bootstrap(ctx context.Context, config BootstrapConfig) error {
	disabled, err := StartupBootstrapDisabled(filepath.Dir(s.db.Path()))
	if err != nil {
		return err
	}
	if disabled {
		return fail(409, "startup_bootstrap_disabled_after_restore")
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	return s.db.Update(func(tx *bolt.Tx) error { return s.seedTx(ctx, tx, config) })
}
func (s *Service) BootstrapTaskTarget(id string) (Target, bool, error) {
	var task Task
	err := s.db.View(func(tx *bolt.Tx) error { var e error; task, e = read[Task](tx, "tasks", id); return e })
	if errors.Is(err, errNotFound) {
		return Target{}, false, nil
	}
	return task.Target, err == nil, err
}
func (s *Service) seedTx(ctx context.Context, tx *bolt.Tx, config BootstrapConfig) error {
	exists := func(kind, id string) bool { return tx.Bucket([]byte(kind)).Get([]byte(id)) != nil }
	save := func(kind, id string, item any) error {
		if err := put(tx, kind, id, item); err != nil {
			return err
		}
		return s.auditTx(tx, nil, "seed.create", kind, id, []string{"registration"})
	}
	for _, d := range config.Devices {
		if exists("devices", d.ID) {
			continue
		}
		if !validID(d.ID) || strings.TrimSpace(d.Name) == "" || d.EndpointID <= 0 {
			return fail(400, "invalid_device")
		}
		if e := s.validateBusinessACL(d.ACL); e != nil {
			return e
		}
		d.MetadataVersion = 1
		if e := save("devices", d.ID, d); e != nil {
			return e
		}
	}
	for _, m := range config.Models {
		if exists("models", m.ID) {
			continue
		}
		if e := validateModelBody(m); e != nil {
			return e
		}
		m.Published = false
		if e := save("models", m.ID, m); e != nil {
			return e
		}
		if e := put(tx, "model-policies", m.ID, s.modelPolicy(tx, m.ID)); e != nil {
			return e
		}
	}
	for _, t := range config.Tasks {
		if exists("tasks", t.ID) {
			continue
		}
		d, e := read[Device](tx, "devices", t.DeviceID)
		if e != nil || t.Target.EndpointID != d.EndpointID || t.Target.StackID <= 0 || !validID(t.ID) || !validID(t.Target.ServiceID) || t.Name == "" || t.Kind == "" {
			return fail(400, "invalid_task")
		}
		if e := validateTaskMetadata(t); e != nil {
			return e
		}
		if e := s.validateBusinessACL(t.ACL); e != nil {
			return e
		}
		if s.opts.ValidateTask != nil {
			if e := s.opts.ValidateTask(ctx, t); e != nil {
				return fail(400, "invalid_task_target")
			}
		}
		rows, e := list[Task](tx, "tasks")
		if e != nil {
			return e
		}
		for _, other := range rows {
			if other.Target.StackID == t.Target.StackID {
				return fail(409, "stack_already_registered")
			}
		}
		t.MetadataVersion = 1
		t.Revision = 1
		for i := range t.Models {
			t.Models[i].State = "unobserved"
			t.Models[i].ObservedPackageID = ""
			t.Models[i].ObservedModelID = ""
			t.Models[i].ObservedModelHash = ""
			t.Models[i].ObservedAt = time.Time{}
		}
		if e := save("tasks", t.ID, t); e != nil {
			return e
		}
	}
	for _, source := range config.Sources {
		if exists("sources", source.ID) {
			continue
		}
		task, e := read[Task](tx, "tasks", source.TaskID)
		if e != nil || task.DeviceID != source.DeviceID || !validID(source.ID) || !validSourceType(source.Type) {
			return fail(400, "invalid_source")
		}
		source.MetadataVersion = 1
		source.Status = "unconfigured"
		source.LastSeenAt = time.Time{}
		source.LastEventAt = time.Time{}
		source.Checkpoint = nil
		source.Coverage = nil
		source.ErrorCode = ""
		if e := save("sources", source.ID, source); e != nil {
			return e
		}
	}
	for _, row := range config.Credentials {
		c := row.Credential
		if exists("credentials", c.ID) {
			continue
		}
		if !validID(c.ID) || c.Name == "" || !tokenHashPattern.MatchString(row.TokenHash) || len(c.DeviceIDs) == 0 || len(c.TaskIDs) == 0 || len(c.SourceIDs) == 0 || len(c.Capabilities) == 0 {
			return fail(400, "invalid_credential")
		}
		for _, cap := range c.Capabilities {
			if !contains([]string{"ingest-events", "ingest-status", "observe-model"}, cap) {
				return fail(400, "invalid_capability")
			}
		}
		for _, id := range c.SourceIDs {
			source, e := read[Source](tx, "sources", id)
			if e != nil || !contains(c.DeviceIDs, source.DeviceID) || !contains(c.TaskIDs, source.TaskID) {
				return fail(400, "invalid_scope")
			}
		}
		for _, id := range c.TaskIDs {
			task, e := read[Task](tx, "tasks", id)
			if e != nil || !contains(c.DeviceIDs, task.DeviceID) {
				return fail(400, "invalid_scope")
			}
		}
		for _, id := range c.DeviceIDs {
			if _, e := read[Device](tx, "devices", id); e != nil {
				return fail(400, "invalid_scope")
			}
		}
		c.CreatedAt = s.opts.Now().UTC()
		c.CreatedByUserID = 0
		c.LastUsedAt = time.Time{}
		c.LastSuccessfulAt = time.Time{}
		c.RevokedAt = time.Time{}
		if e := save("credentials", c.ID, credentialRecord{Credential: c, Hash: row.TokenHash}); e != nil {
			return e
		}
	}
	return nil
}

// Version lookup deliberately ignores runtime/checkpoint writes.
func (s *Service) metadataVersion(tx *bolt.Tx, kind, id string) int {
	if kind == "models" {
		if tx.Bucket([]byte(kind)).Get([]byte(id)) == nil {
			return 0
		}
		return s.modelPolicy(tx, id).MetadataVersion
	}
	raw := tx.Bucket([]byte(kind)).Get([]byte(id))
	if raw == nil {
		return 0
	}
	if kind == "credentials" {
		return 1
	}
	var v struct {
		MetadataVersion int `json:"metadataVersion"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return -1
	}
	return version(v.MetadataVersion)
}
