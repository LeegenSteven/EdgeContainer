package edgestackbusiness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/dataservices"
	"github.com/portainer/portainer/api/datastore"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/api/filesystem"
	stackservice "github.com/portainer/portainer/api/internal/edge/edgestacks"
	"github.com/portainer/portainer/api/roar"
	"go.yaml.in/yaml/v3"
)

const composeFixture = `services:
  voice:
    image: example/old:1
    command: ["python", "server.py"]
    environment:
      MODEL_ID: old
      MODEL_HASH: oldhash
      SECRET_FILE: /run/secrets/keep
      OTHER: unchanged
    ports: ["18621:18621"]
    volumes: ["/voice-data:/data"]
    networks: [private]
networks:
  private: {}
`

func TestComposePatchPreservesUnrelatedConfiguration(t *testing.T) {
	task := edgestack.Task{Target: edgestack.Target{ServiceID: "voice"}, ModelEnvKeys: []string{"MODEL_ID", "MODEL_HASH"}}
	model := edgestack.ModelPackage{Image: "example/new@sha256:" + strings.Repeat("a", 64), Environment: map[string]string{"MODEL_ID": "new", "MODEL_HASH": "newhash"}}
	updated, err := patchCompose([]byte(composeFixture), task, model)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if err := yaml.Unmarshal([]byte(composeFixture), &before); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(updated, &after); err != nil {
		t.Fatal(err)
	}
	service := after["services"].(map[string]any)["voice"].(map[string]any)
	service["image"] = "example/old:1"
	env := service["environment"].(map[string]any)
	env["MODEL_ID"] = "old"
	env["MODEL_HASH"] = "oldhash"
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("unrelated config changed:\n%s", updated)
	}
	model.Environment["SECRET_FILE"] = "/stolen"
	if _, err := patchCompose([]byte(composeFixture), task, model); err == nil {
		t.Fatal("unapproved environment accepted")
	}
	if _, err := patchCompose([]byte(strings.Replace(composeFixture, "networks:\n  private: {}", "  other:\n    image: other\nnetworks:\n  private: {}", 1)), task, model); err == nil {
		t.Fatal("shared stack accepted")
	}
}

func dispatcherFixture(t *testing.T) (*Dispatcher, *datastore.Store, portainer.FileService) {
	t.Helper()
	_, store := datastore.MustNewTestStore(t, true, false)
	fs, err := filesystem.NewService(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := store.Settings().Settings()
	if err != nil {
		t.Fatal(err)
	}
	settings.EnableEdgeComputeFeatures = true
	if err := store.Settings().UpdateSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := store.Endpoint().Create(&portainer.Endpoint{ID: 4, Name: "worker", Type: portainer.EdgeAgentOnDockerEnvironment}); err != nil {
		t.Fatal(err)
	}
	if err := store.EdgeGroup().Create(&portainer.EdgeGroup{ID: 1, Name: "home-workers", EndpointIDs: roar.FromSlice([]portainer.EndpointID{4})}); err != nil {
		t.Fatal(err)
	}
	if err := store.EndpointRelation().Create(&portainer.EndpointRelation{EndpointID: 4, EdgeStacks: map[portainer.EdgeStackID]bool{}}); err != nil {
		t.Fatal(err)
	}
	return &Dispatcher{store: store, files: fs}, store, fs
}
func TestOperatorBootstrapCreatesRealIsolatedStackIdempotently(t *testing.T) {
	dispatcher, store, fs := dispatcherFixture(t)
	service, err := edgestack.Open(filepath.Join(t.TempDir(), "business.db"), edgestack.Options{ValidateTask: dispatcher.ValidateTask})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	config := `{"deployments":[{"name":"edgestack-voice-integration","endpointId":4,"edgeGroupName":"home-workers","compose":` + strconvQuote(composeFixture) + `}],"devices":[{"id":"worker","name":"Worker","endpointId":4,"architecture":"amd64"}],"tasks":[{"id":"voice","deviceId":"worker","name":"Voice","kind":"voice","target":{"endpointId":4,"stackName":"edgestack-voice-integration","serviceId":"voice"},"models":[],"dataLocations":[],"capabilities":["voice-test"],"voiceOrigin":"http://127.0.0.1:18621"}],"sources":[{"id":"events","deviceId":"worker","taskId":"voice","name":"JSONL","type":"voice.jsonl","enabled":true}],"credentials":[]}`
	path := filepath.Join(t.TempDir(), "operator.json")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	stacks := stackservice.NewService(store, fs)
	for i := 0; i < 2; i++ {
		if err := bootstrap(context.Background(), service, store, fs, stacks, path); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.EdgeStack().EdgeStacks()
	if err != nil || len(rows) != 1 {
		t.Fatalf("duplicate bootstrap deployment: %v %v", rows, err)
	}
	stack := rows[0]
	task := edgestack.Task{Target: edgestack.Target{EndpointID: 4, StackID: int(stack.ID), ServiceID: "voice"}, ModelEnvKeys: []string{"MODEL_ID", "MODEL_HASH"}}
	model := edgestack.ModelPackage{Image: "example/new@sha256:" + strings.Repeat("a", 64), Environment: map[string]string{"MODEL_ID": "new", "MODEL_HASH": "newhash"}}
	version, err := dispatcher.Preview(context.Background(), task, model)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Apply(context.Background(), task, model, version); err != nil {
		t.Fatal(err)
	}
	current, err := store.EdgeStack().EdgeStack(stack.ID)
	if err != nil || current.Version != stack.Version+1 {
		t.Fatal("deployment version did not advance")
	}
	if err := dispatcher.Apply(context.Background(), task, model, version); err == nil {
		t.Fatal("stale deployment preview accepted")
	}
	if err := bootstrap(context.Background(), service, store, fs, stacks, path); err != nil {
		t.Fatal(err)
	}
	current, err = store.EdgeStack().EdgeStack(stack.ID)
	if err != nil || current.Version != stack.Version+1 {
		t.Fatal("bootstrap reset changed deployment")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap(context.Background(), service, store, fs, stacks, path); err == nil {
		t.Fatal("public operator configuration accepted")
	}
}
func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

var errInjectedDeployment = errors.New("injected deployment metadata failure")

type failStatusService struct {
	dataservices.EdgeStackStatusService
}

func (failStatusService) Clear(portainer.EdgeStackID, []portainer.EndpointID) error {
	return errInjectedDeployment
}

type failStatusTx struct{ dataservices.DataStoreTx }

func (tx failStatusTx) EdgeStackStatus() dataservices.EdgeStackStatusService {
	return failStatusService{tx.DataStoreTx.EdgeStackStatus()}
}

type failStatusStore struct{ dataservices.DataStore }

func (s failStatusStore) UpdateTx(fn func(dataservices.DataStoreTx) error) error {
	return s.DataStore.UpdateTx(func(tx dataservices.DataStoreTx) error { return fn(failStatusTx{tx}) })
}

type failCommitStore struct {
	dataservices.DataStore
	once bool
}

func (s *failCommitStore) UpdateTx(fn func(dataservices.DataStoreTx) error) error {
	return s.DataStore.UpdateTx(func(tx dataservices.DataStoreTx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if s.once {
			s.once = false
			return errInjectedDeployment
		}
		return nil
	})
}

func TestDispatcherRestoresPreviousFileWhenMetadataOrCommitFails(t *testing.T) {
	for _, failure := range []string{"status", "commit"} {
		t.Run(failure, func(t *testing.T) {
			dispatcher, store, files := dispatcherFixture(t)
			project, err := files.StoreEdgeStackFileFromBytes("9", filesystem.ComposeFileDefaultName, []byte(composeFixture))
			if err != nil {
				t.Fatal(err)
			}
			stack := &portainer.EdgeStack{ID: 9, Name: "edgestack-rollback", EdgeGroups: []portainer.EdgeGroupID{1}, ProjectPath: project, EntryPoint: filesystem.ComposeFileDefaultName, Version: 1, DeploymentType: portainer.EdgeStackDeploymentCompose}
			if err := store.EdgeStack().Create(stack.ID, stack); err != nil {
				t.Fatal(err)
			}
			task := edgestack.Task{Target: edgestack.Target{EndpointID: 4, StackID: 9, ServiceID: "voice"}, ModelEnvKeys: []string{"MODEL_ID"}}
			model := edgestack.ModelPackage{Image: "new@sha256:" + strings.Repeat("a", 64), Environment: map[string]string{"MODEL_ID": "new"}}
			token, err := dispatcher.Preview(context.Background(), task, model)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "status" {
				dispatcher.store = failStatusStore{store}
			} else {
				dispatcher.store = &failCommitStore{DataStore: store, once: true}
			}
			if err := dispatcher.Apply(context.Background(), task, model, token); !errors.Is(err, errInjectedDeployment) {
				t.Fatalf("expected original failure, got %v", err)
			}
			content, err := files.GetFileContent(project, filesystem.ComposeFileDefaultName)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != composeFixture {
				t.Fatalf("failed update left changed file:\n%s", content)
			}
			current, err := store.EdgeStack().EdgeStack(stack.ID)
			if err != nil || current.Version != 1 {
				t.Fatalf("failed update changed metadata: %+v %v", current, err)
			}
			// A fresh, explicitly confirmed selection of the previous published
			// package remains possible after a failed update.
			dispatcher.store = store
			if _, err := dispatcher.Preview(context.Background(), task, model); err != nil {
				t.Fatalf("failure blocked a subsequent preview: %v", err)
			}
		})
	}
}
