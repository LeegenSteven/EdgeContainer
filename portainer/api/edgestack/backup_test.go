package edgestack

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestBusinessSnapshotRestoreRevokesCredentialsAndRequiresReview(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "business.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.db.Update(func(tx *bolt.Tx) error {
		for _, entry := range []struct {
			bucket, id string
			value      any
		}{
			{"devices", "device", Device{ID: "device", EndpointID: 12, ACL: ACL{UserIDs: []int{99}}}},
			{"tasks", "task", Task{ID: "task", DeviceID: "device", State: "registered", Target: Target{EndpointID: 12}, ACL: ACL{UserIDs: []int{99}}}},
			{"sources", "source", Source{ID: "source", TaskID: "task", DeviceID: "device", Enabled: true, Status: "connected", LastSeenAt: time.Now()}},
			{"credentials", "credential", credentialRecord{Credential: Credential{ID: "credential"}, Hash: "fake-test-hash"}},
			{"operations", "operation", Operation{ID: "operation", TaskID: "task", Status: "awaiting_observation"}},
			{"model-validations", "validation", ModelValidation{ID: "validation", PackageID: "package", OperationID: "operation", Status: "validating"}},
			{"model-policies", "package", ModelPolicy{MetadataVersion: 1, LastValidationID: "validation", ValidationStatus: "validating"}},
			{"previews", "preview", previewRecord{Preview: Preview{ID: "preview", ExpiresAt: time.Now().Add(time.Hour)}}},
			{"imports", "import", importRecord{ID: "import", ExpiresAt: time.Now().Add(time.Hour)}},
		} {
			if err := put(tx, entry.bucket, entry.id, entry.value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var snapshot bytes.Buffer
	if err := s.BackupTo(&snapshot); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "business.db")
	if err := os.WriteFile(file, snapshot.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBackup(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RestoreMarker), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(file, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.ApplyRestorePolicy(func(int) bool { return false }, func(ACL) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if err := restored.db.View(func(tx *bolt.Tx) error {
		c, err := read[credentialRecord](tx, "credentials", "credential")
		if err != nil {
			return err
		}
		if !c.Credential.Revoked || c.Hash != "fake-test-hash" {
			t.Fatal("restored credential was revived or record shape changed")
		}
		o, err := read[Operation](tx, "operations", "operation")
		if err != nil {
			return err
		}
		if o.Status != "needs_review" {
			t.Fatal("active operation was not marked for review")
		}
		v, err := read[ModelValidation](tx, "model-validations", "validation")
		if err != nil {
			return err
		}
		policy, err := read[ModelPolicy](tx, "model-policies", "package")
		if err != nil {
			return err
		}
		if v.Status != "needs_review" || policy.ValidationStatus != "needs_review" {
			t.Fatal("restored validation still appears to be in progress")
		}
		source, err := read[Source](tx, "sources", "source")
		if err != nil {
			return err
		}
		if source.Status != "requires_rotation" || !source.LastSeenAt.IsZero() {
			t.Fatal("source appears connected after restore")
		}
		d, err := read[Device](tx, "devices", "device")
		if err != nil {
			return err
		}
		if len(d.UserIDs) != 0 {
			t.Fatal("unresolved user retained access")
		}
		task, err := read[Task](tx, "tasks", "task")
		if err != nil {
			return err
		}
		if len(task.UserIDs) != 0 || task.State != "needs_review" {
			t.Fatal("invalid restored task not isolated")
		}
		if tx.Bucket([]byte("previews")).Stats().KeyN != 0 {
			t.Fatal("stale preview survived restore")
		}
		if tx.Bucket([]byte("imports")).Stats().KeyN != 0 {
			t.Fatal("stale import confirmation survived restore")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, RestoreMarker)); !os.IsNotExist(err) {
		t.Fatal("restore marker not consumed")
	}
	if _, err := os.Stat(filepath.Join(dir, RestoreBootstrapGuard)); err != nil {
		t.Fatal("durable bootstrap guard missing after restore")
	}
	if err := restored.ApplyRestorePolicy(nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreGuardFailurePreservesPendingPolicyAndRetriesSafely(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "business.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := os.WriteFile(filepath.Join(dir, RestoreMarker), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	guard := filepath.Join(dir, RestoreBootstrapGuard)
	if err := os.Mkdir(guard, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyRestorePolicy(nil, nil); err == nil {
		t.Fatal("restore finalized without a durable bootstrap guard")
	}
	if _, err := os.Stat(filepath.Join(dir, RestoreMarker)); err != nil {
		t.Fatal("failed policy lost its pending marker")
	}
	if err := os.Remove(guard); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyRestorePolicy(nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, RestoreMarker)); !os.IsNotExist(err) {
		t.Fatal("successful retry did not consume pending marker")
	}
	info, err := os.Stat(guard)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("successful retry did not preserve private bootstrap guard")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filepath.Join(dir, "business.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	config := BootstrapConfig{Devices: []Device{{ID: "old-device", Name: "Stale operator device", EndpointID: 1}}}
	if err := reopened.Bootstrap(context.Background(), config); err == nil {
		t.Fatal("subsequent startup revived an old operator registration")
	}
}

func TestRestoredGuardStillAllowsExplicitAdministratorImport(t *testing.T) {
	f := setup(t)
	if err := PrepareRestore(filepath.Dir(f.s.db.Path())); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ApplyRestorePolicy(nil, nil); err != nil {
		t.Fatal(err)
	}
	config := BootstrapConfig{Devices: []Device{{ID: "imported-device", Name: "Explicit administrator import", EndpointID: 3}}}
	if err := f.s.Bootstrap(context.Background(), config); err == nil {
		t.Fatal("restored startup accepted implicit seed")
	}
	preview := f.ok(t, "admin", "POST", "/admin/imports/preview", map[string]any{"config": config}, 200)
	f.ok(t, "admin", "POST", "/admin/imports/confirm", map[string]any{"previewId": preview["id"], "confirm": true}, 200)
	f.ok(t, "admin", "GET", "/admin/devices/imported-device", nil, 200)
	if disabled, err := StartupBootstrapDisabled(filepath.Dir(f.s.db.Path())); err != nil || !disabled {
		t.Fatalf("explicit import removed durable startup guard: %v %v", disabled, err)
	}
}

func TestBusinessSnapshotRefusesCrossStoreDispatch(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "business.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.db.Update(func(tx *bolt.Tx) error {
		return put(tx, "operations", "op", Operation{ID: "op", Status: "dispatching"})
	}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := s.BackupTo(&out); !errors.Is(err, ErrBackupBusy) {
		t.Fatalf("expected busy, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("partial snapshot emitted for busy operation")
	}
}

func TestInvalidBusinessBackupRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "business.db")
	if err := os.WriteFile(p, []byte("invalid snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	if ValidateBackup(p) == nil {
		t.Fatal("invalid backup accepted")
	}
}
