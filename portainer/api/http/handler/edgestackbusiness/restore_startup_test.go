package edgestackbusiness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/portainer/portainer/api/datastore"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/api/filesystem"
	stackservice "github.com/portainer/portainer/api/internal/edge/edgestacks"
	"github.com/portainer/portainer/api/internal/testhelpers"
	bolt "go.etcd.io/bbolt"
)

func TestRestoredStartupDoesNotReadOrReplayOperatorDeployments(t *testing.T) {
	_, native := datastore.MustNewTestStore(t, true, false)
	root := native.GetConnection().GetStorePath()
	files, err := filesystem.NewService(root, root)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "edgestack", edgestack.RestoreMarker)
	if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// A restored instance must not even read this old operator file: it could
	// contain deployments absent from the restored snapshot.
	t.Setenv("EDGESTACK_BOOTSTRAP_FILE", filepath.Join(t.TempDir(), "missing-old-operator.json"))
	t.Setenv("EDGESTACK_VOICE_TEST_FILE", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for startup := 0; startup < 2; startup++ {
		service, err := New(ctx, testhelpers.NewTestRequestBouncer(), native, files, nil, nil)
		if err != nil {
			t.Fatalf("restored startup %d: %v", startup+1, err)
		}
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("recovery policy was not applied")
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(marker), edgestack.RestoreBootstrapGuard)); err != nil {
			t.Fatal("durable bootstrap guard missing after restore")
		}
		stacks, err := native.EdgeStack().EdgeStacks()
		if err != nil {
			t.Fatal(err)
		}
		if len(stacks) != 0 {
			t.Fatal("restored startup created a deployment")
		}
	}
}

func TestLegacyRestoredStartupNeverSeedsOperatorDeploymentsAfterRestart(t *testing.T) {
	for _, restored := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "restored-legacy"}[restored], func(t *testing.T) {
			_, native, files := dispatcherFixture(t)
			config := operatorConfig{
				Deployments: []bootstrapDeployment{{Name: "edgestack-restore-guard-test", EndpointID: 4, EdgeGroupName: "home-workers", Compose: composeFixture}},
				BootstrapConfig: edgestack.BootstrapConfig{
					Devices: []edgestack.Device{{ID: "worker", Name: "Fixture worker", EndpointID: 4, Architecture: "amd64"}},
					Tasks:   []edgestack.Task{{ID: "voice", DeviceID: "worker", Name: "Fixture voice", Kind: "voice", Target: edgestack.Target{EndpointID: 4, StackName: "edgestack-restore-guard-test", ServiceID: "voice"}}},
					Sources: []edgestack.Source{{ID: "events", DeviceID: "worker", TaskID: "voice", Name: "Fixture events", Type: "voice.jsonl", Enabled: true}},
				},
			}
			hash := sha256.Sum256([]byte("es_ingest_fake_restore_guard_test"))
			config.Credentials = []edgestack.BootstrapCredential{{Credential: edgestack.Credential{ID: "credential", Name: "Fixture credential", DeviceIDs: []string{"worker"}, TaskIDs: []string{"voice"}, SourceIDs: []string{"events"}, Capabilities: []string{"ingest-events"}}, TokenHash: hex.EncodeToString(hash[:])}}
			encoded, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "operator.json")
			if err := os.WriteFile(path, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("EDGESTACK_BOOTSTRAP_FILE", path)
			t.Setenv("EDGESTACK_VOICE_TEST_FILE", "")
			businessDir := filepath.Join(files.GetDatastorePath(), "edgestack")
			if restored {
				// A legacy archive has no business database; only the durable
				// recovery state survives its deletion/replacement.
				if err := edgestack.PrepareRestore(businessDir); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			for startup := 0; startup < 3; startup++ {
				service, err := New(ctx, testhelpers.NewTestRequestBouncer(), native, files, stackservice.NewService(native, files), nil)
				if err != nil {
					t.Fatalf("startup %d: %v", startup+1, err)
				}
				_, exists, err := service.BootstrapTaskTarget("voice")
				if err != nil || exists == restored {
					service.Close()
					t.Fatalf("startup %d task seeded=%v, restored=%v, error=%v", startup+1, exists, restored, err)
				}
				if err := service.Close(); err != nil {
					t.Fatal(err)
				}
				stacks, err := native.EdgeStack().EdgeStacks()
				want := 1
				if restored {
					want = 0
				}
				if err != nil || len(stacks) != want {
					t.Fatalf("startup %d deployments=%d, want=%d, error=%v", startup+1, len(stacks), want, err)
				}
				db, err := bolt.Open(filepath.Join(businessDir, "business.db"), 0600, &bolt.Options{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				err = db.View(func(tx *bolt.Tx) error {
					for _, kind := range []string{"devices", "tasks", "sources", "credentials"} {
						if count := tx.Bucket([]byte(kind)).Stats().KeyN; count != want {
							t.Errorf("startup %d %s count=%d, want=%d", startup+1, kind, count, want)
						}
					}
					return nil
				})
				if closeErr := db.Close(); err != nil || closeErr != nil {
					t.Fatalf("read restored registrations: %v %v", err, closeErr)
				}
			}
		})
	}
}
