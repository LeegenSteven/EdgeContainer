package compose

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/compose/v2/pkg/api"
	"github.com/docker/docker/api/types/container"
	"github.com/portainer/portainer/pkg/libstack"
	"github.com/stretchr/testify/require"
)

func TestZeroReplicaDeploymentObservesWholeProject(t *testing.T) {
	const project = "edge_blue_vision"
	file := createFile(t, t.TempDir(), "compose.yaml", "x-model: &model\n  image: local/model\n  scale: ${REPLICAS}\nservices:\n  model:\n    <<: *model\n")
	for _, tc := range []struct {
		state, scope, id string
		legacy, complete bool
	}{
		{complete: true}, {state: "exited", scope: "blue", id: "7", complete: true},
		{state: "running", scope: "blue", id: "7"}, {state: "created", scope: "blue", id: "7"},
		{state: "paused", scope: "blue", id: "7"}, {state: "restarting", scope: "blue", id: "7"},
		{state: "exited", scope: "other", id: "7"}, {state: "exited", scope: "blue", id: "8"},
		{state: "exited", scope: "blue", id: "7", legacy: true},
	} {
		t.Run(tc.state+"-"+tc.scope+"-"+tc.id, func(t *testing.T) {
			var lists atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/_ping":
					w.Header().Set("API-Version", "1.51")
				case strings.HasSuffix(r.URL.Path, "/containers/json") && r.Method == http.MethodGet:
					lists.Add(1)
					if r.URL.Query().Get("all") != "1" || !strings.Contains(r.URL.Query().Get("filters"), api.ProjectLabel+"="+project) {
						t.Error("container query did not cover the whole project")
					}
					rows := []container.Summary{}
					if tc.state != "" {
						labels := map[string]string{api.ProjectLabel: project, libstack.EdgeStackScopeLabel: tc.scope, libstack.EdgeStackScopedIDLabel: tc.id}
						if tc.legacy {
							labels[PortainerEdgeStackLabel] = ""
						}
						rows = append(rows, container.Summary{ID: "model", Labels: labels, State: tc.state})
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(rows)
				default:
					t.Errorf("unexpected Docker operation %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			c, err := NewScopedComposeDeployer("blue")
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
			defer cancel()
			result := c.WaitForDeployment(ctx, file, libstack.DeployOptions{Options: libstack.Options{
				ProjectName: project, Host: "tcp://" + strings.TrimPrefix(server.URL, "http://"), Env: []string{"REPLICAS=0"},
			}, EdgeStackID: 7})
			require.Positive(t, lists.Load())
			if tc.complete {
				require.Empty(t, result.ErrorMsg)
				require.Equal(t, libstack.StatusCompleted, result.Status)
			} else {
				require.NotEmpty(t, result.ErrorMsg)
			}
		})
	}
}

func TestPositiveReplicasNeedAllDeclaredServices(t *testing.T) {
	const name = "edge_blue_vision"
	file := createFile(t, t.TempDir(), "compose.yaml", "services:\n  model:\n    image: local/model\n    deploy:\n      replicas: 2\n  logs:\n    image: local/logs\n")
	service := &scopedComposeTestService{containers: map[string][]api.ContainerSummary{}}
	c := scopedTestDeployer(t, "blue", service)
	options := libstack.DeployOptions{Options: libstack.Options{ProjectName: name}, EdgeStackID: 7}
	for _, services := range [][]string{nil, {"model", "model"}, {"model", "logs"}, {"model", "model", "logs"}} {
		service.containers[name] = nil
		for _, svc := range services {
			service.containers[name] = append(service.containers[name], api.ContainerSummary{Service: svc, State: "running", Labels: map[string]string{api.ProjectLabel: name, libstack.EdgeStackScopeLabel: "blue", libstack.EdgeStackScopedIDLabel: "7"}})
		}
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		result := c.WaitForDeployment(ctx, file, options)
		cancel()
		if len(services) == 3 {
			require.Empty(t, result.ErrorMsg)
			require.Equal(t, libstack.StatusRunning, result.Status)
		} else {
			require.NotEmpty(t, result.ErrorMsg)
		}
	}
	bad := createFile(t, t.TempDir(), "compose.yaml", "services:\n  model:\n    image: local/model\n    scale: 0\n    deploy: {replicas: 1}\n")
	require.NotEmpty(t, c.WaitForDeployment(t.Context(), bad, options).ErrorMsg)
}
