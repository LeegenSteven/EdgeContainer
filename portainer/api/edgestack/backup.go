package edgestack

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

var ErrBackupBusy = errors.New("business deployment is being dispatched")

const RestoreMarker = "restore-pending"

// BackupTo must be called while the shared backup gate holds its write lease.
// Tx.WriteTo exports a consistent snapshot, never a live database file copy.
func (s *Service) BackupTo(w io.Writer) error {
	return s.db.View(func(tx *bolt.Tx) error {
		operations, err := list[Operation](tx, "operations")
		if err != nil {
			return err
		}
		for _, op := range operations {
			if op.Status == "dispatching" {
				return ErrBackupBusy
			}
		}
		_, err = tx.WriteTo(w)
		return err
	})
}

// ValidateBackup checks a candidate before any currently open store is closed.
func ValidateBackup(path string) error {
	db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return errors.New("invalid business database backup")
	}
	defer db.Close()
	return db.View(func(tx *bolt.Tx) error {
		for _, name := range []string{"devices", "tasks", "sources", "events", "credentials", "models"} {
			if tx.Bucket([]byte(name)) == nil {
				return errors.New("incomplete business database backup")
			}
		}
		var checkErr error
		for err := range tx.Check() {
			if err != nil {
				checkErr = errors.New("corrupt business database backup")
			}
		}
		if checkErr != nil {
			return checkErr
		}
		return nil
	})
}

// ApplyRestorePolicy runs after startup seeding and before the API is exposed.
// Credential revocation is deliberately repeated until the marker is removed.
// This prevents restoring a formerly revoked credential from an older backup.
func (s *Service) ApplyRestorePolicy(endpointExists func(int) bool, aclExists func(ACL) bool) error {
	marker := filepath.Join(filepath.Dir(s.db.Path()), RestoreMarker)
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	// Upgrade older pending-marker restores before consuming the marker. This
	// guard must remain after successful recovery and across future restarts.
	if err := preserveRestoreBootstrapGuard(filepath.Dir(marker)); err != nil {
		return err
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		creds, err := list[credentialRecord](tx, "credentials")
		if err != nil {
			return err
		}
		for _, credential := range creds {
			credential.Credential.Revoked = true
			if err := put(tx, "credentials", credential.Credential.ID, credential); err != nil {
				return err
			}
		}
		sources, err := list[Source](tx, "sources")
		if err != nil {
			return err
		}
		for _, source := range sources {
			source.Status, source.ErrorCode = "requires_rotation", "restored_credential_rotation_required"
			source.LastSeenAt = time.Time{}
			if err := put(tx, "sources", source.ID, source); err != nil {
				return err
			}
		}
		operations, err := list[Operation](tx, "operations")
		if err != nil {
			return err
		}
		for _, op := range operations {
			if op.Status == "dispatching" || op.Status == "awaiting_observation" || op.Status == "queued" {
				op.Status, op.ErrorCode = "needs_review", "restored_operation_requires_review"
				op.UpdatedAt = s.opts.Now().UTC()
				if err := put(tx, "operations", op.ID, op); err != nil {
					return err
				}
			}
		}
		if tx.Bucket([]byte("model-validations")) != nil {
			validations, err := list[ModelValidation](tx, "model-validations")
			if err != nil {
				return err
			}
			for _, validation := range validations {
				if validation.Status != "validating" {
					continue
				}
				validation.Status, validation.ErrorCode = "needs_review", "restored_validation_requires_review"
				validation.UpdatedAt = s.opts.Now().UTC()
				if err := put(tx, "model-validations", validation.ID, validation); err != nil {
					return err
				}
				if policy, err := read[ModelPolicy](tx, "model-policies", validation.PackageID); err == nil && policy.LastValidationID == validation.ID {
					policy.ValidationStatus = "needs_review"
					policy.MetadataVersion++
					if err := put(tx, "model-policies", validation.PackageID, policy); err != nil {
						return err
					}
				}
			}
		}
		devices, err := list[Device](tx, "devices")
		if err != nil {
			return err
		}
		invalid := map[string]bool{}
		for _, device := range devices {
			if (endpointExists != nil && !endpointExists(device.EndpointID)) || (aclExists != nil && !aclExists(device.ACL)) {
				device.ACL = ACL{}
				invalid[device.ID] = true
				if err := put(tx, "devices", device.ID, device); err != nil {
					return err
				}
			}
		}
		tasks, err := list[Task](tx, "tasks")
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if invalid[task.DeviceID] || (endpointExists != nil && !endpointExists(task.Target.EndpointID)) || (aclExists != nil && !aclExists(task.ACL)) {
				task.ACL, task.State = ACL{}, "needs_review"
				if err := put(tx, "tasks", task.ID, task); err != nil {
					return err
				}
			}
		}
		// Old previews never become a new deployment after recovery.
		if err := tx.DeleteBucket([]byte("previews")); err != nil && err != bolt.ErrBucketNotFound {
			return err
		}
		_, err = tx.CreateBucketIfNotExists([]byte("previews"))
		if err != nil {
			return err
		}
		if tx.Bucket([]byte("imports")) != nil {
			if err := tx.DeleteBucket([]byte("imports")); err != nil {
				return err
			}
			_, err = tx.CreateBucket([]byte("imports"))
		}
		return err
	})
	if err != nil {
		return err
	}
	if err := os.Remove(marker); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(marker))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
