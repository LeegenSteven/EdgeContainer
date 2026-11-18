package edgestackbusiness

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/api/filesystem"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

type validationRecordingDispatcher struct {
	*Dispatcher
	ctx context.Context
}

func (d *validationRecordingDispatcher) Preview(ctx context.Context, task edgestack.Task, model edgestack.ModelPackage) (string, error) {
	d.ctx = ctx
	return d.Dispatcher.Preview(ctx, task, model)
}

type replicaValidationFixture struct {
	t          *testing.T
	dispatcher *validationRecordingDispatcher
	files      portainer.FileService
	stack      *portainer.EdgeStack
	task       edgestack.Task
	model      edgestack.ModelPackage
	handler    http.Handler
}

func newReplicaValidationFixture(t *testing.T, content string, testTask bool) *replicaValidationFixture {
	t.Helper()
	dispatcher, store, files := dispatcherFixture(t)
	project, err := files.StoreEdgeStackFileFromBytes("9", filesystem.ComposeFileDefaultName, []byte(content))
	require.NoError(t, err)
	stack := &portainer.EdgeStack{ID: 9, Name: "isolated-validation", EdgeGroups: []portainer.EdgeGroupID{1}, ProjectPath: project, EntryPoint: filesystem.ComposeFileDefaultName, Version: 1, DeploymentType: portainer.EdgeStackDeploymentCompose}
	require.NoError(t, store.EdgeStack().Create(stack.ID, stack))
	recording := &validationRecordingDispatcher{Dispatcher: dispatcher}
	service, err := edgestack.Open(filepath.Join(t.TempDir(), "business.db"), edgestack.Options{
		Dispatcher: recording, ValidateTask: dispatcher.ValidateTask,
		EndpointAccess: func(*http.Request, int) bool { return true },
		Authenticate: func(*http.Request) (edgestack.Principal, error) {
			return edgestack.Principal{UserID: 1, Admin: true}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Close() })
	f := &replicaValidationFixture{t: t, dispatcher: recording, files: files, stack: stack, handler: service.Handler()}
	f.task = edgestack.Task{ID: "test-task", DeviceID: "worker", Name: "Validation", Kind: "vision", TestTask: testTask, RuntimeBackend: "rknn", TemplateID: "validation-template",
		Target: edgestack.Target{EndpointID: 4, StackID: 9, ServiceID: "voice"}, Models: []edgestack.ModelBinding{{Slot: "vision"}}, Capabilities: []string{"replace-model"}, ModelEnvKeys: []string{"MODEL_ID"}}
	f.model = edgestack.ModelPackage{ID: "candidate", Name: "Candidate", Version: "1", Kind: "vision", Slot: "vision", TaskKinds: []string{"vision"}, Architectures: []string{"amd64"}, RuntimeBackend: "rknn", TemplateIDs: []string{"validation-template"},
		Immutable: true, Image: "example/new@sha256:" + strings.Repeat("a", 64), ModelID: "fixture-model", ModelHash: "sha256:" + strings.Repeat("b", 64), Environment: map[string]string{"MODEL_ID": "new"}}
	f.call("/admin/devices", edgestack.Device{ID: "worker", Name: "Worker", EndpointID: 4, Architecture: "amd64"}, 200)
	f.call("/admin/tasks", f.task, 200)
	f.call("/admin/models", f.model, 200)
	return f
}

func (f *replicaValidationFixture) call(path string, body any, status int) map[string]any {
	f.t.Helper()
	data, err := json.Marshal(body)
	require.NoError(f.t, err)
	request := httptest.NewRequest("POST", "/api/edgestack"+path, bytes.NewReader(data))
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	require.Equal(f.t, status, response.Code, response.Body.String())
	var result map[string]any
	require.NoError(f.t, json.Unmarshal(response.Body.Bytes(), &result))
	return result
}

func (f *replicaValidationFixture) content() []byte {
	f.t.Helper()
	content, err := f.files.GetFileContent(f.stack.ProjectPath, f.stack.EntryPoint)
	require.NoError(f.t, err)
	return content
}

func stoppedReplicaCompose(counts string) string {
	return strings.Replace(composeFixture, "    command:", counts+"    command:", 1)
}

func TestInternalValidationRestartsOnlyItsIsolatedService(t *testing.T) {
	for _, counts := range []string{"    deploy:\n      replicas: 0\n    scale: 0\n", "    scale: 0\n"} {
		t.Run(strings.TrimSpace(counts), func(t *testing.T) {
			before := stoppedReplicaCompose(counts)
			before = strings.Replace(before, "    networks: [private]", "    networks: [private]\n    secrets: [keep]", 1) + "secrets:\n  keep:\n    content: fixture-only-no-authority\n"
			f := newReplicaValidationFixture(t, before, true)
			preview := f.call("/admin/models/candidate/validation-preview", map[string]any{"taskId": f.task.ID, "slot": "vision", "revision": 1}, 200)
			require.True(t, edgestack.IsModelValidation(f.dispatcher.ctx, f.task.ID, f.model.ID))
			require.Equal(t, before, string(f.content()), "preview must not activate a service")
			f.call("/admin/models/candidate/validations", map[string]any{"previewId": preview["id"], "confirm": true}, 202)
			var old, current map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(before), &old))
			require.NoError(t, yaml.Unmarshal(f.content(), &current))
			service := current["services"].(map[string]any)["voice"].(map[string]any)
			require.Equal(t, 1, service["deploy"].(map[string]any)["replicas"])
			require.Equal(t, 1, service["scale"])
			require.Equal(t, f.model.Image, service["image"])
			require.Equal(t, "new", service["environment"].(map[string]any)["MODEL_ID"])
			// Restore only the allowed changes; all mounts, ports, secrets,
			// networks, commands and other environment must match the original.
			original := old["services"].(map[string]any)["voice"].(map[string]any)
			service["image"] = original["image"]
			service["environment"].(map[string]any)["MODEL_ID"] = "old"
			service["scale"] = original["scale"]
			if original["deploy"] == nil {
				delete(service, "deploy")
			} else {
				service["deploy"] = original["deploy"]
			}
			require.True(t, reflect.DeepEqual(old, current), "validation changed unrelated service configuration")
		})
	}
}

func TestOrdinaryReplacementAndUnmarkedTestTaskRemainStopped(t *testing.T) {
	for _, testTask := range []bool{false, true} {
		before := stoppedReplicaCompose("    deploy:\n      replicas: 0\n    scale: 0\n")
		f := newReplicaValidationFixture(t, before, testTask)
		ctx := context.Background()
		token, err := f.dispatcher.Preview(ctx, f.task, f.model)
		require.NoError(t, err)
		require.NoError(t, f.dispatcher.Apply(ctx, f.task, f.model, token))
		var document map[string]any
		require.NoError(t, yaml.Unmarshal(f.content(), &document))
		service := document["services"].(map[string]any)["voice"].(map[string]any)
		require.Equal(t, 0, service["deploy"].(map[string]any)["replicas"])
		require.Equal(t, 0, service["scale"])
	}
}

func TestValidationContextCannotActivateAnotherTaskOrPackage(t *testing.T) {
	before := stoppedReplicaCompose("    deploy:\n      replicas: 0\n")
	f := newReplicaValidationFixture(t, before, true)
	f.call("/admin/models/candidate/validation-preview", map[string]any{"taskId": f.task.ID, "slot": "vision", "revision": 1}, 200)
	for _, wrongTask := range []bool{true, false} {
		task, model := f.task, f.model
		if wrongTask {
			task.ID = "another-test-task"
		} else {
			model.ID = "another-package"
		}
		updated, err := patchComposeContext(f.dispatcher.ctx, []byte(before), task, model)
		require.NoError(t, err)
		var document map[string]any
		require.NoError(t, yaml.Unmarshal(updated, &document))
		service := document["services"].(map[string]any)["voice"].(map[string]any)
		require.Equal(t, 0, service["deploy"].(map[string]any)["replicas"], "misbound validation intent must not activate a different target")
	}
}

func TestValidationRejectsUnsafeReplicaCountsInPreviewAndApply(t *testing.T) {
	for name, counts := range map[string]string{
		"multiple_replicas": "    deploy:\n      replicas: 2\n",
		"multiple_scale":    "    scale: 2\n",
		"negative":          "    deploy:\n      replicas: -1\n",
		"inconsistent":      "    deploy:\n      replicas: 0\n    scale: 1\n",
		"anchored":          "    deploy:\n      replicas: &count 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			before := stoppedReplicaCompose(counts)
			f := newReplicaValidationFixture(t, before, true)
			f.call("/admin/models/candidate/validation-preview", map[string]any{"taskId": f.task.ID, "slot": "vision", "revision": 1}, 409)
			require.True(t, edgestack.IsModelValidation(f.dispatcher.ctx, f.task.ID, f.model.ID))
			err := f.dispatcher.Apply(f.dispatcher.ctx, f.task, f.model, version(f.stack, []byte(before)))
			require.Error(t, err)
			require.Equal(t, before, string(f.content()), "failed validation must retain the stopped/native configuration")
		})
	}
}

func TestValidationCannotBeRequestedForOrdinaryTaskOrThroughJSON(t *testing.T) {
	before := stoppedReplicaCompose("    deploy:\n      replicas: 0\n")
	f := newReplicaValidationFixture(t, before, false)
	f.call("/admin/models/candidate/validation-preview", map[string]any{"taskId": f.task.ID, "slot": "vision", "revision": 1}, 409)
	f.call("/tasks/"+f.task.ID+"/model-change-preview", map[string]any{"packageId": "candidate", "slot": "vision", "revision": 1, "modelValidation": true}, 400)
	require.Equal(t, before, string(f.content()))
}

func TestValidationRejectsInheritedServiceAndDeployReplicaCounts(t *testing.T) {
	for name, content := range map[string]string{
		"service_merge": "x-defaults: &defaults\n  deploy:\n    replicas: 2\n" + strings.Replace(composeFixture, "  voice:\n", "  voice:\n    <<: *defaults\n", 1),
		"deploy_merge":  "x-defaults: &defaults\n  replicas: 2\n" + stoppedReplicaCompose("    deploy:\n      <<: *defaults\n"),
	} {
		t.Run(name, func(t *testing.T) {
			f := newReplicaValidationFixture(t, content, true)
			f.call("/admin/models/candidate/validation-preview", map[string]any{"taskId": f.task.ID, "slot": "vision", "revision": 1}, 409)
			err := f.dispatcher.Apply(f.dispatcher.ctx, f.task, f.model, version(f.stack, []byte(content)))
			require.Error(t, err)
			require.True(t, strings.Contains(err.Error(), "inheritance") || strings.Contains(err.Error(), "explicit deploy mapping"), err.Error())
			require.Equal(t, content, string(f.content()))
		})
	}
}
