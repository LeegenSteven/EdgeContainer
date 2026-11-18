package edgestackbusiness

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/datastore"
	"github.com/portainer/portainer/api/edge"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/pkg/libstack"
	"github.com/stretchr/testify/require"
)

func scopeLabels(scope, id, service, name string) map[string]string {
	return map[string]string{libstack.EdgeStackScopeLabel: scope, libstack.EdgeStackScopedIDLabel: id, composeServiceLabel: service, composeProjectLabel: libstack.EdgeStackProjectName(scope, name)}
}

func TestScopedMountProviderRejectsCollidingLegacyAndOtherScope(t *testing.T) {
	p, client, task := mountFixture()
	p.executionScope = "acceptance"
	task.Target.StackName = "vision"
	owned := scopeLabels("acceptance", "4", "voice", "vision")
	legacy := client.rows[0]
	other := container.Summary{ID: "other-server", State: "running", Labels: scopeLabels("production", "4", "voice", "vision")}
	client.rows = []container.Summary{legacy, other, {ID: "registered-container", State: "running", Labels: owned}}
	client.actual.Config.Labels = owned
	checks := p.Verify(context.Background(), task, []edgestack.MountRequest{{ContainerPath: "/models"}})
	require.True(t, checks[0].Verified)
	require.Equal(t, "registered-container", client.inspectedID)
	query := client.options.Filters.Get("label")
	require.Contains(t, query, libstack.EdgeStackScopedIDLabel+"=4")
	require.Contains(t, query, libstack.EdgeStackScopeLabel+"=acceptance")
	require.Contains(t, query, composeProjectLabel+"=edge_acceptance_vision")
	require.NotContains(t, query, edgeStackLabel+"=4")
}

func TestScopedMountProviderRechecksInspectIdentity(t *testing.T) {
	for _, mutate := range []string{"scope", "stack", "project", "service", "legacy_dual_label"} {
		t.Run(mutate, func(t *testing.T) {
			p, c, task := mountFixture()
			p.executionScope = "acceptance"
			task.Target.StackName = "vision"
			c.rows[0].Labels = scopeLabels("acceptance", "4", "voice", "vision")
			c.actual.Config.Labels = scopeLabels("acceptance", "4", "voice", "vision")
			switch mutate {
			case "scope":
				c.actual.Config.Labels[libstack.EdgeStackScopeLabel] = "production"
			case "stack":
				c.actual.Config.Labels[libstack.EdgeStackScopedIDLabel] = "5"
			case "project":
				c.actual.Config.Labels[composeProjectLabel] = "edge_production_vision"
			case "service":
				c.actual.Config.Labels[composeServiceLabel] = "another"
			case "legacy_dual_label":
				c.actual.Config.Labels[edgeStackLabel] = "4"
			}
			observation := p.snapshot(context.Background(), task)
			require.Equal(t, "container_identity_mismatch", observation.reason)
		})
	}
}

func TestScopedMountProviderUsesCurrentNativeNameAndInvalidatesNameCache(t *testing.T) {
	p, c, task := mountFixture()
	p.executionScope = "acceptance"
	task.Target.StackName = "untrusted-stale-name"
	name := "vision"
	p.stackName = func(int) (string, error) { return name, nil }
	c.rows[0].Labels = scopeLabels("acceptance", "4", "voice", name)
	c.actual.Config.Labels = c.rows[0].Labels
	require.Empty(t, p.snapshot(context.Background(), task).reason)
	name = "renamed"
	c.rows[0].Labels = scopeLabels("acceptance", "4", "voice", name)
	c.actual.Config.Labels = c.rows[0].Labels
	require.Empty(t, p.snapshot(context.Background(), task).reason)
	require.EqualValues(t, 2, c.lists.Load())
	require.Contains(t, c.options.Filters.Get("label"), composeProjectLabel+"=edge_acceptance_renamed")
}

func TestScopeStopProofRejectsLegacyForeignAndWrongProject(t *testing.T) {
	target := edgestack.Target{StackID: 4, StackName: "vision", ServiceID: "voice"}
	identity, err := containerTarget("acceptance", target)
	require.NoError(t, err)
	for _, scope := range []string{"production", "acceptance"} {
		row := container.Summary{State: "exited", Labels: scopeLabels(scope, "4", "voice", "vision")}
		require.Equal(t, scope == "acceptance", executionContainersStopped([]container.Summary{row}, identity))
	}
	legacy := container.Summary{State: "exited", Labels: map[string]string{edgeStackLabel: "4", composeServiceLabel: "voice"}}
	require.False(t, executionContainersStopped([]container.Summary{legacy}, identity))
	require.False(t, deliveryContainersStopped([]container.Summary{{State: "exited", Labels: scopeLabels("acceptance", "4", "voice", "vision")}}, target))
	wrongProject := scopeLabels("acceptance", "4", "voice", "another")
	require.False(t, executionContainersStopped([]container.Summary{{State: "exited", Labels: wrongProject}}, identity))
}

func TestScopedManagedStopNeverStopsLegacyOrOtherScope(t *testing.T) {
	for _, foreign := range []string{"legacy", "production", "inspect_changed"} {
		t.Run(foreign, func(t *testing.T) {
			a, task, _ := executionAuthorityFixture(t, "acceptance")
			task.Target.StackName = "validation"
			labels := scopeLabels("acceptance", "1", "voice", "validation")
			if foreign == "legacy" {
				labels = map[string]string{edgeStackLabel: "1", composeServiceLabel: "voice"}
			}
			if foreign == "production" {
				labels = scopeLabels("production", "1", "voice", "validation")
			}
			client := &executionStopClient{fakeMountClient: fakeMountClient{rows: []container.Summary{{ID: "collision", State: "paused", Labels: labels}}, actual: container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "collision", State: &container.State{Status: "paused", Running: true, Paused: true}}, Config: &container.Config{Labels: scopeLabels("production", "1", "voice", "validation")}}}}
			a.mounts = &mountProvider{executionScope: "acceptance", newClient: func(context.Context, edgestack.Task) (mountClient, error) { return client, nil }}
			lease := edgestack.DeliveryExecutionLease{ResourceGroup: "board", LeaseID: "lease", FencingToken: 1, ControlEpoch: a.service.DeliveryControlEpoch(), ExpiresAt: time.Now().Add(time.Minute)}
			a.client.Transport = executionRoundTripper(func(*http.Request) (*http.Response, error) {
				return executionResponse(map[string]any{"valid": true, "lease_id": "lease", "group_id": "board", "expires_at": time.Now().Add(time.Minute)}), nil
			})
			require.ErrorContains(t, a.stopExecutionContainers(context.Background(), task, lease), "container_identity_changed")
			require.Zero(t, client.stops)
			require.Contains(t, client.options.Filters.Get("label"), libstack.EdgeStackScopeLabel+"=acceptance")
		})
	}
}

func TestExecutionScopeCapabilityMustMatchAndRevokesStaleObservation(t *testing.T) {
	a, _, _ := executionAuthorityFixture(t, "acceptance")
	a.executionScope = "acceptance"
	require.Error(t, a.ObserveCapability(context.Background(), 4, edge.ExecutionGrantsCapability, ""))
	require.NoError(t, a.ObserveCapability(context.Background(), 4, edge.ExecutionGrantsCapability, "acceptance"))
	require.NoError(t, a.Preview(context.Background(), 4))
	require.Error(t, a.ObserveCapability(context.Background(), 4, edge.ExecutionGrantsCapability, "production"))
	require.Error(t, a.Preview(context.Background(), 4))
	t.Setenv("EDGESTACK_EXECUTION_SCOPE", "Invalid Scope")
	_, err := newDeliveryExecutionAuthority(a.service, a.store)
	require.Error(t, err)
}

func TestScopedManagedStopConfirmsOnlyInspectedOwnedTarget(t *testing.T) {
	a, task, _ := executionAuthorityFixture(t, "acceptance")
	task.Target.StackName = "validation"
	labels := scopeLabels("acceptance", "1", "voice", "validation")
	client := &executionStopClient{fakeMountClient: fakeMountClient{rows: []container.Summary{{ID: "owned", State: "paused", Labels: labels}}, actual: container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "owned", State: &container.State{Running: true, Paused: true}}, Config: &container.Config{Labels: labels}}}}
	a.mounts = &mountProvider{executionScope: "acceptance", newClient: func(context.Context, edgestack.Task) (mountClient, error) { return client, nil }}
	lease := edgestack.DeliveryExecutionLease{ResourceGroup: "board", LeaseID: "lease", FencingToken: 1, ControlEpoch: a.service.DeliveryControlEpoch(), ExpiresAt: time.Now().Add(time.Minute)}
	a.client.Transport = executionRoundTripper(func(*http.Request) (*http.Response, error) {
		return executionResponse(map[string]any{"valid": true, "lease_id": "lease", "group_id": "board", "expires_at": time.Now().Add(time.Minute)}), nil
	})
	require.NoError(t, a.stopExecutionContainers(context.Background(), task, lease))
	require.Equal(t, 1, client.stops)
	require.Equal(t, "owned", client.inspectedID)
	require.EqualValues(t, 3, client.lists.Load())
}

func TestStopProofInspectsStoppedRowsAndActualStateInEveryScope(t *testing.T) {
	for _, scope := range []string{"", "acceptance"} {
		identity, err := containerTarget(scope, edgestack.Target{StackID: 4, StackName: "vision", ServiceID: "voice"})
		require.NoError(t, err)
		labels := scopeLabels(scope, "4", "voice", "vision")
		if scope == "" {
			labels = map[string]string{edgeStackLabel: "4", composeServiceLabel: "voice"}
		}
		c := &fakeMountClient{actual: container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "owned", State: &container.State{Status: "running", Running: true}}, Config: &container.Config{Labels: labels}}}
		rows := []container.Summary{{ID: "owned", State: "exited", Labels: labels}}
		stopped, err := inspectExecutionContainersStopped(context.Background(), c, rows, identity)
		require.NoError(t, err)
		require.False(t, stopped, "an exited list row cannot override a running inspect")
		c.actual.State = &container.State{Status: "exited"}
		stopped, err = inspectExecutionContainersStopped(context.Background(), c, rows, identity)
		require.NoError(t, err)
		require.True(t, stopped)
		c.actual.Config.Labels = map[string]string{edgeStackLabel: "another", composeServiceLabel: "voice"}
		_, err = inspectExecutionContainersStopped(context.Background(), c, rows, identity)
		require.ErrorContains(t, err, "container_identity_changed")
		require.EqualValues(t, 3, c.inspects.Load())
	}
}

func TestScopeChangeCannotReleasePhysicalLeaseAfterEmptyList(t *testing.T) {
	a, _, _ := executionAuthorityFixture(t, "blue")
	c := &executionStopClient{}
	a.mounts = &mountProvider{executionScope: "green", newClient: func(context.Context, edgestack.Task) (mountClient, error) { return c, nil }}
	a.executionScope = "green"
	a.client.Transport = executionRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("scope drift must not reach lease authority")
		return nil, nil
	})
	plan := edgestack.DeliveryExecutionStopPlan{ResourceGroup: "board", LeaseID: "lease", Stopped: true}
	require.ErrorContains(t, a.confirmExecutionStop(context.Background(), &plan), "execution_scope_identity_changed")
	require.Zero(t, c.lists.Load(), "empty new-scope inventory is not stop evidence for the original scope")
	require.Zero(t, c.stops)
}

func TestExecutionGrantDirectRouteRequiresMatchingScope(t *testing.T) {
	_, store := datastore.MustNewTestStore(t, true, false)
	require.NoError(t, store.Endpoint().Create(&portainer.Endpoint{ID: 4, Type: portainer.EdgeAgentOnDockerEnvironment, EdgeID: "edge-identity"}))
	grant := edge.ExecutionGrant{ResourceGroup: "board", LeaseID: "lease", FencingToken: 1, ControlEpoch: "epoch", OperationID: "op", ExpectedStackVersion: 1, ExpiresAt: time.Now().Add(time.Minute), Action: "deploy"}
	body, _ := json.Marshal(map[string]any{"grant": grant, "action": "deploy"})
	for _, scope := range []string{"", "production", "acceptance"} {
		called := false
		h := NewExecutionGrantHandler(store, func(context.Context, int, int, string, edge.ExecutionGrant) error { called = true; return nil }, "acceptance")
		r := httptest.NewRequest(http.MethodPost, "/api/endpoints/4/edge/stacks/1/execution/verify", bytes.NewReader(body))
		r.Header.Set(portainer.PortainerAgentEdgeIDHeader, "edge-identity")
		r.Header.Set(edge.ExecutionGrantsHeader, edge.ExecutionGrantsCapability)
		if scope != "" {
			r.Header.Set(libstack.EdgeStackExecutionScopeHeader, scope)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, scope == "acceptance", called)
		if called {
			require.Equal(t, http.StatusNoContent, w.Code)
		} else {
			require.Equal(t, http.StatusForbidden, w.Code)
		}
	}
}
