package edgestack

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/portainer/portainer/pkg/libstack"
	bolt "go.etcd.io/bbolt"
)

const executionIdentityBucket = "delivery-execution-identity"
const executionScopeMarker = "execution-scope.identity"

type executionScopeIdentity struct {
	Version int    `json:"version"`
	Scope   string `json:"scope"`
}

func readExecutionScope(tx *bolt.Tx) (executionScopeIdentity, bool, error) {
	var identity executionScopeIdentity
	bucket := tx.Bucket([]byte(executionIdentityBucket))
	if bucket == nil || bucket.Get([]byte("scope")) == nil {
		return identity, false, nil
	}
	err := json.Unmarshal(bucket.Get([]byte("scope")), &identity)
	if err != nil || identity.Version != 1 || libstack.ValidateEdgeStackScope(identity.Scope) != nil {
		return identity, true, errors.New("execution_scope_identity_invalid")
	}
	return identity, true, nil
}

func (s *Service) readExecutionScopeMarker() (executionScopeIdentity, bool, error) {
	var identity executionScopeIdentity
	path := filepath.Join(filepath.Dir(s.db.Path()), executionScopeMarker)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return identity, false, nil
	}
	if err != nil {
		return identity, false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024 {
		return identity, true, errors.New("execution_scope_marker_invalid")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return identity, true, err
	}
	if json.Unmarshal(raw, &identity) != nil || identity.Version != 1 || libstack.ValidateEdgeStackScope(identity.Scope) != nil {
		return identity, true, errors.New("execution_scope_marker_invalid")
	}
	return identity, true, nil
}

func hasLegacyExecutionRecords(tx *bolt.Tx) (bool, error) {
	for _, name := range []string{executionBindingsBucket, "delivery-execution-stop-previews"} {
		if bucket := tx.Bucket([]byte(name)); bucket != nil && bucket.Stats().KeyN != 0 {
			return true, nil
		}
	}
	if tx.Bucket([]byte(executionLeasesBucket)) != nil {
		leases, err := list[DeliveryExecutionLease](tx, executionLeasesBucket)
		if err != nil {
			return false, err
		}
		for _, lease := range leases {
			if !lease.Released {
				return true, nil
			}
		}
	}
	return false, nil
}

// The DB identity travels with new backups. The separate immutable marker is
// intentionally not included by backupBusiness/restoreBusiness, preserving the
// local namespace even when an older backup predating this metadata is restored.
func (s *Service) initExecutionScope() error {
	expected := executionScopeIdentity{Version: 1, Scope: s.opts.ExecutionScope}
	if err := libstack.ValidateEdgeStackScope(expected.Scope); err != nil {
		return err
	}
	marker, exists, err := s.readExecutionScopeMarker()
	if err != nil {
		return err
	}
	if exists && marker != expected {
		return errors.New("execution_scope_identity_changed")
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		identity, bound, err := readExecutionScope(tx)
		if err != nil {
			return err
		}
		if bound {
			if identity != expected {
				return errors.New("execution_scope_identity_changed")
			}
			return nil
		}
		legacy, err := hasLegacyExecutionRecords(tx)
		if err != nil {
			return err
		}
		if legacy && expected.Scope != "" {
			return errors.New("legacy_execution_scope_migration_refused")
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(executionIdentityBucket)); err != nil {
			return err
		}
		return put(tx, executionIdentityBucket, "scope", expected)
	})
	if err != nil {
		return err
	}
	if !exists {
		path := filepath.Join(filepath.Dir(s.db.Path()), executionScopeMarker)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(expected)
		_, err = file.Write(raw)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		directory, err := os.Open(filepath.Dir(path))
		if err != nil {
			return err
		}
		err = directory.Sync()
		directory.Close()
		if err != nil {
			return err
		}
	}
	return s.VerifyDeliveryExecutionScope(expected.Scope)
}

// Recheck both authorities before every execution/recovery side effect. Never
// recreate a missing identity in a live process after a restore or DB mutation.
func (s *Service) VerifyDeliveryExecutionScope(scope string) error {
	if scope != s.opts.ExecutionScope {
		return errors.New("execution_scope_identity_changed")
	}
	expected := executionScopeIdentity{Version: 1, Scope: scope}
	marker, exists, err := s.readExecutionScopeMarker()
	if err != nil {
		return err
	}
	if !exists || marker != expected {
		return errors.New("execution_scope_identity_changed")
	}
	return s.db.View(func(tx *bolt.Tx) error {
		identity, bound, err := readExecutionScope(tx)
		if err != nil {
			return err
		}
		if !bound || identity != expected {
			return errors.New("execution_scope_identity_changed")
		}
		return nil
	})
}
