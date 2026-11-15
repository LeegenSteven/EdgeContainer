package edgestack

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var buckets = []string{"devices", "tasks", "sources", "events", "credentials", "previews", "operations", "summaries"}
var errNotFound = errors.New("not found")

type Service struct {
	cloud        *cloudManager
	closed       chan struct{}
	closeOnce    sync.Once
	closeErr     error
	db           *bolt.DB
	opts         Options
	modelMu      sync.Mutex
	summarySlots chan struct{}
	summaryUsers sync.Map
}

func Open(path string, opts Options) (*Service, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, err
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Service{db: db, opts: opts, summarySlots: make(chan struct{}, 2), closed: make(chan struct{})}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range append(buckets, "models", "admin-meta", "model-policies", "model-validations", "audit", "imports") {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = s.migrateAdminSchema(); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.initCloudSettings(filepath.Dir(path)); err != nil {
		db.Close()
		return nil, err
	}
	if _, markerErr := os.Stat(filepath.Join(filepath.Dir(path), RestoreMarker)); os.IsNotExist(markerErr) {
		if err = s.Prune(); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Service) Close() error {
	s.closeOnce.Do(func() { close(s.closed); s.closeErr = s.db.Close() })
	return s.closeErr
}

func (s *Service) RunRetention(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.closed:
			return
		case <-ticker.C:
			_ = s.Prune()
		}
	}
}

func put(tx *bolt.Tx, bucket, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return tx.Bucket([]byte(bucket)).Put([]byte(id), b)
}

func read[T any](tx *bolt.Tx, bucket, id string) (T, error) {
	var v T
	b := tx.Bucket([]byte(bucket)).Get([]byte(id))
	if b == nil {
		return v, errNotFound
	}
	return v, json.Unmarshal(b, &v)
}

func list[T any](tx *bolt.Tx, bucket string) ([]T, error) {
	out := make([]T, 0)
	err := tx.Bucket([]byte(bucket)).ForEach(func(_, v []byte) error {
		var row T
		if err := json.Unmarshal(v, &row); err != nil {
			return err
		}
		out = append(out, row)
		return nil
	})
	return out, err
}

func (s *Service) Prune() error {
	if s.opts.BackgroundWriteLease != nil {
		unlock, err := s.opts.BackgroundWriteLease()
		if err != nil {
			return err
		}
		defer unlock()
	}
	if err := s.expireOperations(); err != nil {
		return err
	}
	cutoff := s.opts.Now().Add(-Retention)
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, name := range []string{"events", "summaries", "previews", "operations", "audit", "imports"} {
			b := tx.Bucket([]byte(name))
			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				remove := false
				switch name {
				case "events":
					var e Event
					if err := json.Unmarshal(v, &e); err != nil {
						return err
					}
					remove = e.OccurredAt.Before(cutoff)
				case "summaries":
					var r summaryRecord
					if err := json.Unmarshal(v, &r); err != nil {
						return err
					}
					remove = r.CreatedAt.Before(cutoff)
				case "previews":
					var p previewRecord
					if err := json.Unmarshal(v, &p); err != nil {
						return err
					}
					remove = p.Preview.ExpiresAt.Before(s.opts.Now().Add(-time.Hour))
				case "operations":
					var o Operation
					if err := json.Unmarshal(v, &o); err != nil {
						return err
					}
					remove = o.UpdatedAt.Before(s.opts.Now().Add(-90 * 24 * time.Hour))
				case "audit":
					var a AuditEntry
					if err := json.Unmarshal(v, &a); err != nil {
						return err
					}
					remove = a.OccurredAt.Before(s.opts.Now().Add(-90 * 24 * time.Hour))
				case "imports":
					var a importRecord
					if err := json.Unmarshal(v, &a); err != nil {
						return err
					}
					remove = a.ExpiresAt.Before(s.opts.Now().Add(-time.Hour))
				}
				if remove {
					if err := c.Delete(); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

func (s *Service) expireOperationsBackground() error {
	if s.opts.BackgroundWriteLease != nil {
		unlock, err := s.opts.BackgroundWriteLease()
		if err != nil {
			return nil
		}
		defer unlock()
	}
	return s.expireOperations()
}

func (s *Service) expireOperations() error {
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	return s.db.Update(func(tx *bolt.Tx) error {
		operations, err := list[Operation](tx, "operations")
		if err != nil {
			return err
		}
		for _, op := range operations {
			if (op.Status == "dispatching" || op.Status == "awaiting_observation") && s.opts.Now().Sub(op.UpdatedAt) > 15*time.Minute {
				if op.Status == "dispatching" {
					op.ErrorCode = "deployment_outcome_unknown"
				} else {
					op.ErrorCode = "model_observation_timeout"
				}
				op.Status = "failed"
				op.UpdatedAt = s.opts.Now().UTC()
				if err := put(tx, "operations", op.ID, op); err != nil {
					return err
				}
				if err := s.finishValidation(tx, op, Source{}, observation{}); err != nil {
					return err
				}
				task, err := read[Task](tx, "tasks", op.TaskID)
				if err == nil && task.Revision == op.Revision {
					for i := range task.Models {
						if task.Models[i].Slot == op.Slot && task.Models[i].PackageID == op.PackageID {
							task.Models[i].State = "unconfirmed"
						}
					}
					if err := put(tx, "tasks", task.ID, task); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

func newID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func contains[T comparable](values []T, value T) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func allows(p Principal, acl ACL) bool {
	if p.Admin {
		return true
	}
	if contains(acl.UserIDs, p.UserID) {
		return true
	}
	for _, id := range p.TeamIDs {
		if contains(acl.TeamIDs, id) {
			return true
		}
	}
	return false
}
