package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/portainer/portainer/api/dataservices"
	"github.com/portainer/portainer/api/datastore"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/api/http/offlinegate"
)

type restoreCloseSpy struct {
	dataservices.DataStore
	closed bool
}

type restoreCloseFailure struct{ dataservices.DataStore }

func (s *restoreCloseFailure) Close() error { return os.ErrPermission }

func TestRestoreFailureAfterClosingBusinessAlwaysStopsServer(t *testing.T) {
	_, source := datastore.MustNewTestStore(t, true, false)
	gate := offlinegate.NewOfflineGate()
	file, err := CreateBackupArchive("", gate, source, source.GetConnection().GetStorePath())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	_, target := datastore.MustNewTestStore(t, true, false)
	root := target.GetConnection().GetStorePath()
	business, err := edgestack.Open(filepath.Join(root, "edgestack", "business.db"), edgestack.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer business.Close()
	shutdowns := 0
	err = RestoreArchive(bytes.NewReader(data), "", root, gate, &restoreCloseFailure{DataStore: target}, func() { shutdowns++ }, business)
	if err == nil || shutdowns != 1 {
		t.Fatalf("closed-store restore failure did not shut down: error=%v shutdowns=%d", err, shutdowns)
	}
	var out bytes.Buffer
	if business.BackupTo(&out) == nil {
		t.Fatal("test did not reach closed business database stage")
	}
	if _, err := os.Stat(filepath.Join(root, "edgestack", edgestack.RestoreMarker)); err != nil {
		t.Fatal("failed restore lost recovery marker")
	}
	if _, err := os.Stat(filepath.Join(root, "edgestack", edgestack.RestoreBootstrapGuard)); err != nil {
		t.Fatal("failed restore lost durable bootstrap guard")
	}
}

func (s *restoreCloseSpy) Close() error { s.closed = true; return s.DataStore.Close() }

func TestRestoreMarkerFailureDoesNotCloseOrReplaceStores(t *testing.T) {
	_, source := datastore.MustNewTestStore(t, true, false)
	gate := offlinegate.NewOfflineGate()
	archive, err := CreateBackupArchive("", gate, source, source.GetConnection().GetStorePath())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	_, target := datastore.MustNewTestStore(t, true, false)
	root := target.GetConnection().GetStorePath()
	business, err := edgestack.Open(filepath.Join(root, "edgestack", "business.db"), edgestack.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer business.Close()
	// A directory at the marker filename deterministically rejects the atomic
	// marker rename, simulating failure without relying on root-sensitive modes.
	if err := os.Mkdir(filepath.Join(root, "edgestack", edgestack.RestoreMarker), 0700); err != nil {
		t.Fatal(err)
	}
	spy := &restoreCloseSpy{DataStore: target}
	shutdown := false
	if err := RestoreArchive(bytes.NewReader(data), "", root, gate, spy, func() { shutdown = true }, business); err == nil {
		t.Fatal("restore continued without durable marker")
	}
	if spy.closed || shutdown {
		t.Fatal("restore closed main database before recovery marker was durable")
	}
	var out bytes.Buffer
	if err := business.BackupTo(&out); err != nil {
		t.Fatalf("business database closed on failed preparation: %v", err)
	}
}

func TestFullArchiveRoundTripIncludesBusinessAndRequiresCredentialRotation(t *testing.T) {
	for _, password := range []string{"", "fake-test-backup-password"} {
		t.Run(map[bool]string{true: "encrypted", false: "plain"}[password != ""], func(t *testing.T) {
			_, native := datastore.MustNewTestStore(t, true, false)
			root := native.GetConnection().GetStorePath()
			admin := edgestack.Options{Authenticate: func(*http.Request) (edgestack.Principal, error) {
				return edgestack.Principal{UserID: 1, Admin: true}, nil
			}}
			business, err := edgestack.Open(filepath.Join(root, "edgestack", "business.db"), admin)
			if err != nil {
				t.Fatal(err)
			}
			defer business.Close()
			hash := sha256.Sum256([]byte("es_ingest_fake_snapshot_test"))
			if err := business.Bootstrap(context.Background(), edgestack.BootstrapConfig{
				Devices:     []edgestack.Device{{ID: "device-test", Name: "Fixture device", EndpointID: 1}},
				Tasks:       []edgestack.Task{{ID: "task-test", DeviceID: "device-test", Name: "Fixture task", Kind: "voice", Target: edgestack.Target{EndpointID: 1, StackID: 1, ServiceID: "voice"}}},
				Sources:     []edgestack.Source{{ID: "source-test", DeviceID: "device-test", TaskID: "task-test", Name: "Fixture source", Type: "voice.jsonl", Enabled: true}},
				Credentials: []edgestack.BootstrapCredential{{Credential: edgestack.Credential{ID: "credential-test", Name: "Fixture credential", DeviceIDs: []string{"device-test"}, TaskIDs: []string{"task-test"}, SourceIDs: []string{"source-test"}, Capabilities: []string{"ingest-events", "ingest-status"}}, TokenHash: hex.EncodeToString(hash[:])}},
			}); err != nil {
				t.Fatal(err)
			}
			secrets := filepath.Join(root, "edgestack", "secrets")
			if err := os.MkdirAll(secrets, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(secrets, "fake.key"), []byte("fake-private-file-not-in-archive"), 0600); err != nil {
				t.Fatal(err)
			}
			gate := offlinegate.NewOfflineGate()
			archivePath, err := CreateBackupArchive(password, gate, native, root, business)
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(archivePath)
			if err != nil {
				t.Fatal(err)
			}
			_, restoredNative := datastore.MustNewTestStore(t, true, false)
			restoredRoot := restoredNative.GetConnection().GetStorePath()
			before, err := edgestack.Open(filepath.Join(restoredRoot, "edgestack", "business.db"), admin)
			if err != nil {
				t.Fatal(err)
			}
			defer before.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := RestoreArchive(bytes.NewReader(contents), password, restoredRoot, gate, restoredNative, cancel, before); err != nil {
				t.Fatal(err)
			}
			if ctx.Err() == nil {
				t.Fatal("restored Server was not scheduled to shut down")
			}
			if _, err := os.Stat(filepath.Join(restoredRoot, "edgestack", "secrets", "fake.key")); !os.IsNotExist(err) {
				t.Fatal("private key entered system archive")
			}
			restored, err := edgestack.Open(filepath.Join(restoredRoot, "edgestack", "business.db"), admin)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if err := restored.ApplyRestorePolicy(nil, nil); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"ingest-credentials", "sources"} {
				w := httptest.NewRecorder()
				restored.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/edgestack/admin/"+path, nil))
				if w.Code != 200 {
					t.Fatalf("restored %s: %d", path, w.Code)
				}
				var data struct {
					Items []map[string]any `json:"items"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
					t.Fatal(err)
				}
				if len(data.Items) != 1 {
					t.Fatalf("restored %s missing registered record", path)
				}
				if path == "ingest-credentials" && data.Items[0]["revoked"] != true {
					t.Fatal("restored credential remains valid")
				}
				if path == "sources" && data.Items[0]["status"] != "requires_rotation" {
					t.Fatal("restored source does not request rotation")
				}
			}
		})
	}
}

func TestBusinessBackupRestoreExcludesSecretFiles(t *testing.T) {
	live := t.TempDir()
	s, err := edgestack.Open(filepath.Join(live, "edgestack", "business.db"), edgestack.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	secretDir := filepath.Join(live, "edgestack", "secrets")
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "active.key"), []byte("fake-private-test-key"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := t.TempDir()
	if err := backupBusiness(snapshot, s); err != nil {
		t.Fatal(err)
	}
	if err := validateBusinessRestore(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(snapshot, "edgestack", "secrets")); !os.IsNotExist(err) {
		t.Fatal("secret directory entered backup")
	}
	destination := t.TempDir()
	secrets := filepath.Join(destination, "edgestack", "secrets")
	if err := os.MkdirAll(secrets, 0700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(secrets, "active.key")
	if err := os.WriteFile(key, []byte("existing-host-test-key"), 0600); err != nil {
		t.Fatal(err)
	}
	// Even an archive containing an extra secret path cannot replace host keys.
	injected := filepath.Join(snapshot, "edgestack", "secrets")
	if err := os.MkdirAll(injected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(injected, "active.key"), []byte("injected-test-key"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := restoreBusiness(snapshot, destination); err != nil {
		t.Fatal(err)
	}
	if err := edgestack.ValidateBackup(filepath.Join(destination, "edgestack", "business.db")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "existing-host-test-key" {
		t.Fatal("archive replaced private host key")
	}
	info, err := os.Stat(filepath.Join(destination, "edgestack", "business.db"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("restored business file is not private")
	}
	if _, err := os.Stat(filepath.Join(destination, "edgestack", edgestack.RestoreMarker)); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyBackupRemovesStaleBusinessDataButKeepsPrivateKeys(t *testing.T) {
	dest := t.TempDir()
	dir := filepath.Join(dest, "edgestack")
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "business.db"), []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "secrets", "active.key")
	if err := os.WriteFile(key, []byte("fake-host-key"), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := t.TempDir()
	if err := validateBusinessRestore(legacy); err != nil {
		t.Fatal(err)
	}
	if err := restoreBusiness(legacy, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "business.db")); !os.IsNotExist(err) {
		t.Fatal("stale business data retained for legacy restore")
	}
	if _, err := os.Stat(key); err != nil {
		t.Fatal("private key removed during restore")
	}
	if _, err := os.Stat(filepath.Join(dir, edgestack.RestoreBootstrapGuard)); err != nil {
		t.Fatal("legacy restore removed durable bootstrap guard with stale database")
	}
}
