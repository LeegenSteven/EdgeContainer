package edgestackbusiness

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/api/filesystem"
	"github.com/portainer/portainer/pkg/libstack"
	"github.com/stretchr/testify/require"
)

type changingHandoffClient struct {
	*executionStopClient
	beforeList func()
}

func (c *changingHandoffClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	c.beforeList()
	return c.executionStopClient.ContainerList(ctx, options)
}

func TestValidationStopRechecksWorkerAuthorizationAfterInspect(t *testing.T) {
	a, task, _ := executionAuthorityFixture(t)
	client := &executionStopClient{fakeMountClient: fakeMountClient{rows: []container.Summary{{ID: "test", State: "running", Labels: map[string]string{edgeStackLabel: "1", composeServiceLabel: "voice"}}}}}
	a.mounts = &mountProvider{newClient: func(context.Context, edgestack.Task) (mountClient, error) { return client, nil }}
	lease := edgestack.DeliveryExecutionLease{LeaseID: "lease", ResourceGroup: "board", FencingToken: 1, ControlEpoch: a.service.DeliveryControlEpoch(), ExpiresAt: time.Now().Add(time.Minute)}
	a.client.Transport = executionRoundTripper(func(*http.Request) (*http.Response, error) {
		return executionResponse(map[string]any{"valid": true, "lease_id": "lease", "group_id": "board", "expires_at": lease.ExpiresAt}), nil
	})
	revoked := errors.New("claim expired during Docker inspection")
	err := a.stopExecutionContainers(t.Context(), task, lease, func() error { return revoked })
	require.ErrorIs(t, err, revoked)
	require.EqualValues(t, 1, client.inspects.Load())
	require.Zero(t, client.stops)
}

func TestValidationHandoffRequiresVersionedStopAndFreshDockerEvidence(t *testing.T) {
	a, task, dbPath := executionAuthorityFixture(t, "blue")
	stack, err := a.store.EdgeStack().EdgeStack(1)
	require.NoError(t, err)
	stack.DeploymentType = portainer.EdgeStackDeploymentCompose
	stack.EdgeGroups = []portainer.EdgeGroupID{1}
	stack.EntryPoint = filesystem.ComposeFileDefaultName
	stack.ProjectPath, err = a.dispatcher.files.StoreEdgeStackFileFromBytes("1", stack.EntryPoint, []byte(stoppedReplicaCompose("    scale: 0\n")))
	require.NoError(t, err)
	require.NoError(t, a.store.EdgeStack().UpdateEdgeStack(1, stack))
	project := libstack.EdgeStackProjectName("blue", stack.Name)
	client := &executionStopClient{}
	a.mounts = &mountProvider{executionScope: "blue", stackName: func(int) (string, error) { return stack.Name, nil },
		newClient: func(context.Context, edgestack.Task) (mountClient, error) { return client, nil }}
	setStatus := func(kind portainer.EdgeStackStatusType, version int) {
		t.Helper()
		require.NoError(t, a.store.EdgeStackStatus().Update(1, 4, &portainer.EdgeStackStatusForEnv{EndpointID: 4, Status: []portainer.EdgeStackDeploymentStatus{{Type: kind, Version: version}}}))
	}
	check := func() error { return a.proveValidationStopped(t.Context(), task, "board") }
	for _, item := range []portainer.EdgeStackDeploymentStatus{
		{Type: portainer.EdgeStackStatusAcknowledged, Version: 1},
		{Type: portainer.EdgeStackStatusCompleted, Version: 0},
		{Type: portainer.EdgeStackStatusCompleted, Version: 2},
		{Type: portainer.EdgeStackStatusError, Version: 1},
	} {
		setStatus(item.Type, item.Version)
		require.Error(t, check())
	}
	require.Zero(t, client.lists.Load(), "an unconfirmed revision must fail before contacting Docker")
	setStatus(portainer.EdgeStackStatusCompleted, 1)
	require.NoError(t, check())
	require.EqualValues(t, 2, client.lists.Load())
	require.True(t, client.options.All)
	require.Equal(t, []string{composeProjectLabel + "=" + project}, client.options.Filters.Get("label"))
	labels := scopeLabels("blue", "1", "voice", stack.Name)
	calls := 0
	originalFactory := a.mounts.newClient
	a.mounts.newClient = func(context.Context, edgestack.Task) (mountClient, error) {
		return &changingHandoffClient{client, func() {
			calls++
			if calls == 2 {
				client.rows = []container.Summary{{ID: "ctr", State: "running", Labels: labels}}
			}
		}}, nil
	}
	require.Error(t, check(), "a container appeared between the two reads")
	a.mounts.newClient = originalFactory
	for _, state := range []string{"running", "paused", "restarting", "created", "removing"} {
		client.rows = []container.Summary{{ID: "ctr", State: state, Labels: labels}}
		require.Error(t, check(), state)
	}
	client.rows[0].State = "exited"
	client.inspectErr = errors.New("tunnel interrupted")
	require.Error(t, check())
	client.inspectErr = nil
	client.listErr = errors.New("unavailable")
	require.Error(t, check())
	client.listErr = nil
	for _, bad := range []map[string]string{scopeLabels("other", "1", "voice", stack.Name), scopeLabels("blue", "2", "voice", stack.Name), scopeLabels("blue", "1", "other", stack.Name), scopeLabels("blue", "1", "voice", "other")} {
		client.rows[0].Labels = bad
		require.Error(t, check())
	}
	client.rows[0].Labels = labels
	require.NoError(t, check())
	require.Zero(t, client.stops, "the initial handoff must be entirely read-only")
	_, err = a.dispatcher.files.StoreEdgeStackFileFromBytes("1", stack.EntryPoint, []byte(composeFixture))
	require.NoError(t, err)
	require.Error(t, check(), "a one-shot workload can also report Completed")

	lease := edgestack.DeliveryExecutionLease{ResourceGroup: "board", LeaseID: "resident", ControlEpoch: a.service.DeliveryControlEpoch(), FencingToken: 3, ActiveOperationID: "stop-plan", Released: true}
	require.NoError(t, a.service.DeliverySaveExecutionLease(lease))
	require.Error(t, check(), "a released flag alone cannot authorize an active desired count")
	plan := edgestack.DeliveryExecutionStopPlan{ID: "stop-plan", Status: "released", Stopped: true, ControlEpoch: lease.ControlEpoch, ResourceGroup: "board", LeaseID: "resident", Targets: []edgestack.DeliveryExecutionStopTarget{{TaskID: task.ID, StackID: 1, Version: 1}}}
	require.NoError(t, a.service.DeliverySaveExecutionStopPlan(plan))
	require.NoError(t, check(), "repeat a confirmed manual handoff without stopping again")
	stack.Version++
	require.NoError(t, a.store.EdgeStack().UpdateEdgeStack(1, stack))
	require.Error(t, check(), "a stop of an older stack cannot authorize the new version")
	job := edgestack.DeliveryJob{ValidationDeviceID: task.DeviceID, ValidationTaskID: task.ID}
	_, _, err = a.validationHandoffTarget(job)
	require.NoError(t, err)
	business := task
	business.ID = "production"
	business.Target.StackID = 2
	business.TestTask = false
	require.NoError(t, a.service.Bootstrap(t.Context(), edgestack.BootstrapConfig{Tasks: []edgestack.Task{business}}))
	_, _, err = a.validationHandoffTarget(job)
	require.ErrorContains(t, err, "contains_other_task")
	// Corrupt only the marker in this fixture's temporary directory.
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dbPath), "execution-scope.identity"), []byte("{}"), 0600))
	_, _, err = a.validationHandoffTarget(job)
	require.ErrorContains(t, err, "scope")
}
