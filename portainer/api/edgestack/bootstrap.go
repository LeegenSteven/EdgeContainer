package edgestack

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// BootstrapConfig is only imported from an explicitly configured, private
// operator file at startup. It is never exposed as a public HTTP endpoint.
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
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, d := range config.Devices {
			if !validID(d.ID) || strings.TrimSpace(d.Name) == "" || d.EndpointID <= 0 {
				return errors.New("invalid bootstrap device")
			}
			if old, e := read[Device](tx, "devices", d.ID); e == nil && old.EndpointID != d.EndpointID {
				return errors.New("bootstrap device target changed")
			}
			if e := put(tx, "devices", d.ID, d); e != nil {
				return e
			}
		}
		for _, m := range config.Models {
			parts := strings.Split(m.Image, "@")
			if !validID(m.ID) || m.Name == "" || m.Version == "" || m.Slot == "" || !m.Immutable || !digest.MatchString(m.ModelHash) || len(m.Architectures) == 0 || len(m.TaskKinds) == 0 || len(parts) != 2 || !digest.MatchString(parts[1]) {
				return errors.New("invalid bootstrap model package")
			}
			if old, e := read[ModelPackage](tx, "models", m.ID); e == nil {
				// Publication is mutable operator policy, not package identity.
				// Reimporting an old startup file must not revive withdrawn
				// packages or overwrite an explicit subsequent publication.
				candidate := m
				candidate.Published = old.Published
				if !reflect.DeepEqual(old, candidate) {
					return errors.New("bootstrap cannot change immutable package")
				}
				continue
			}
			if e := put(tx, "models", m.ID, m); e != nil {
				return e
			}
		}
		for _, t := range config.Tasks {
			d, e := read[Device](tx, "devices", t.DeviceID)
			if e != nil || t.Target.EndpointID != d.EndpointID || t.Target.StackID <= 0 || !validID(t.ID) || !validID(t.Target.ServiceID) || t.Name == "" || t.Kind == "" {
				return errors.New("invalid bootstrap task")
			}
			if t.VoiceOrigin != "" {
				u, e := url.Parse(t.VoiceOrigin)
				if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
					return errors.New("invalid bootstrap voice origin")
				}
			}
			if s.opts.ValidateTask != nil {
				if e := s.opts.ValidateTask(ctx, t); e != nil {
					return e
				}
			}
			rows, e := list[Task](tx, "tasks")
			if e != nil {
				return e
			}
			for _, other := range rows {
				if other.ID != t.ID && other.Target.StackID == t.Target.StackID {
					return errors.New("stack already registered")
				}
			}
			if old, e := read[Task](tx, "tasks", t.ID); e == nil {
				if old.Target != t.Target || old.DeviceID != t.DeviceID {
					return errors.New("bootstrap task target changed")
				}
				// Startup must not reset a user-confirmed desired model or trusted
				// runtime observation, nor increment revisions without a change.
				t.Models = old.Models
				t.Revision = old.Revision
				t.State = old.State
				if !reflect.DeepEqual(old, t) {
					t.Revision++
				}
			} else {
				t.Revision = 1
				for i := range t.Models {
					t.Models[i].ObservedPackageID = ""
					t.Models[i].ObservedModelID = ""
					t.Models[i].ObservedModelHash = ""
					t.Models[i].ObservedAt = time.Time{}
					t.Models[i].State = "unobserved"
				}
			}
			if e := put(tx, "tasks", t.ID, t); e != nil {
				return e
			}
		}
		for _, source := range config.Sources {
			task, e := read[Task](tx, "tasks", source.TaskID)
			if e != nil || task.DeviceID != source.DeviceID || !validID(source.ID) || source.Type == "" {
				return errors.New("invalid bootstrap source")
			}
			if old, e := read[Source](tx, "sources", source.ID); e == nil {
				if old.TaskID != source.TaskID || old.DeviceID != source.DeviceID {
					return errors.New("bootstrap source target changed")
				}
				old.Name = source.Name
				old.Enabled = source.Enabled
				old.Type = source.Type
				source = old
			} else {
				source.Status = "unconfigured"
				source.LastEventAt = time.Time{}
				source.LastSeenAt = time.Time{}
				source.Checkpoint = nil
				source.Coverage = nil
			}
			if e := put(tx, "sources", source.ID, source); e != nil {
				return e
			}
		}
		for _, row := range config.Credentials {
			c := row.Credential
			if !validID(c.ID) || c.Name == "" || !tokenHashPattern.MatchString(row.TokenHash) || len(c.DeviceIDs) == 0 || len(c.TaskIDs) == 0 || len(c.SourceIDs) == 0 || len(c.Capabilities) == 0 {
				return errors.New("invalid bootstrap credential")
			}
			for _, cap := range c.Capabilities {
				if !contains([]string{"ingest-events", "ingest-status", "observe-model"}, cap) {
					return errors.New("invalid bootstrap capability")
				}
			}
			for _, id := range c.SourceIDs {
				source, e := read[Source](tx, "sources", id)
				if e != nil || !contains(c.DeviceIDs, source.DeviceID) || !contains(c.TaskIDs, source.TaskID) {
					return errors.New("invalid bootstrap credential scope")
				}
			}
			for _, id := range c.TaskIDs {
				task, e := read[Task](tx, "tasks", id)
				if e != nil || !contains(c.DeviceIDs, task.DeviceID) {
					return errors.New("invalid bootstrap task scope")
				}
			}
			for _, id := range c.DeviceIDs {
				if _, e := read[Device](tx, "devices", id); e != nil {
					return errors.New("invalid bootstrap device scope")
				}
			}
			// A revoked token stays revoked when the same operator file is read
			// again. Rotating its hash explicitly creates fresh credential state.
			if old, e := read[credentialRecord](tx, "credentials", c.ID); e == nil && old.Hash == row.TokenHash {
				c.Revoked = old.Credential.Revoked
			}
			if e := put(tx, "credentials", c.ID, credentialRecord{Credential: c, Hash: row.TokenHash}); e != nil {
				return e
			}
		}
		return nil
	})
}
