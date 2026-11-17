package stack

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/cli/cli/command"
	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/portainer/agent/deployer"
	"github.com/portainer/agent/edge/client"
	"github.com/portainer/agent/internals/mocks"
	"github.com/portainer/portainer/api/edge"
	"github.com/portainer/portainer/pkg/libstack"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type executionTestClient struct {
	client.PortainerClient
	verify func(context.Context, int, int, string, edge.ExecutionGrant) error
}

func (c executionTestClient) VerifyExecutionGrant(ctx context.Context, id, version int, action string, grant edge.ExecutionGrant) error {
	return c.verify(ctx, id, version, action, grant)
}

func executionTestStack() *edgeStack {
	return &edgeStack{StackPayload: edge.StackPayload{ID: 4, Name: "managed", Version: 2, ManagedExecution: true, PrePullImage: true, ExecutionGrant: &edge.ExecutionGrant{
		ResourceGroup: "board-a", LeaseID: "lease-a", FencingToken: 7, ControlEpoch: "epoch-a", OperationID: "operation-a", ExpectedStackVersion: 2, ExpiresAt: time.Now().Add(time.Minute), Action: "deploy",
	}}}
}

func TestManagedFencePersistsAcrossRestartAndCannotDowngrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fences.json")
	var calls atomic.Int32
	verifier := executionTestClient{verify: func(context.Context, int, int, string, edge.ExecutionGrant) error { calls.Add(1); return nil }}
	manager := &StackManager{engineType: EngineTypeDockerStandalone, executionFences: &executionFenceStore{path: path}, portainerClient: verifier}
	stack := executionTestStack()
	require.NoError(t, manager.verifyManagedExecution(t.Context(), stack, "deploy"))
	restarted := &StackManager{engineType: EngineTypeDockerStandalone, executionFences: &executionFenceStore{path: path}, portainerClient: verifier}
	stack.ExecutionGrant.FencingToken = 6
	require.ErrorContains(t, restarted.verifyManagedExecution(t.Context(), stack, "deploy"), "stale_fence")
	require.Equal(t, int32(1), calls.Load())
	stack.ExecutionGrant.FencingToken = 7
	stack.ExecutionGrant.LeaseID = "other-lease"
	require.ErrorContains(t, restarted.verifyManagedExecution(t.Context(), stack, "deploy"), "stale_fence")
	stack.ExecutionGrant = nil
	stack.ManagedExecution = false
	require.ErrorContains(t, restarted.verifyManagedExecution(t.Context(), stack, "deploy"), "grant_required")
	require.ErrorContains(t, restarted.executionFences.rejectsNormalStack("edge_managed"), "requires_execution_grant")
}

func TestManagedMutationRejectsMissingExpiredOrRefusedGrant(t *testing.T) {
	for _, scenario := range []string{"missing", "expired", "wrong_version", "authority_refused", "remove_with_deploy_grant"} {
		t.Run(scenario, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			deployerMock := mocks.NewMockDeployer(ctrl)
			server := mocks.NewMockPortainerClient(ctrl)
			server.EXPECT().SetEdgeStackStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			manager := &StackManager{engineType: EngineTypeDockerStandalone, executionFences: &executionFenceStore{}, deployer: deployerMock, portainerClient: executionTestClient{PortainerClient: server, verify: func(context.Context, int, int, string, edge.ExecutionGrant) error {
				if scenario == "authority_refused" {
					return errors.New("lease_expired")
				}
				return nil
			}}}
			stack := executionTestStack()
			switch scenario {
			case "missing":
				stack.ExecutionGrant = nil
			case "expired":
				stack.ExecutionGrant.ExpiresAt = time.Now().Add(-time.Second)
			case "wrong_version":
				stack.ExecutionGrant.ExpectedStackVersion++
			}
			if scenario == "remove_with_deploy_grant" {
				manager.deleteStack(t.Context(), stack, "edge_managed", "stack.yml")
			} else {
				require.Error(t, manager.pullImages(t.Context(), stack, "edge_managed", "stack.yml"))
				manager.deployStack(t.Context(), stack, "edge_managed", "stack.yml")
			}
			require.Equal(t, StatusError, stack.Status)
		})
	}
}

func TestManagedDeploymentCancelsWhenAuthorityIsLost(t *testing.T) {
	ctrl := gomock.NewController(t)
	server := mocks.NewMockPortainerClient(ctrl)
	server.EXPECT().SetEdgeStackStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	var checks atomic.Int32
	deployerMock := mocks.NewMockDeployer(ctrl)
	deployerMock.EXPECT().Deploy(gomock.Any(), "edge_managed", gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ string, _ []string, _ deployer.DeployOptions) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			t.Error("execution did not stop after losing authority")
			return errors.New("timeout")
		}
	})
	manager := &StackManager{engineType: EngineTypeDockerStandalone, executionFences: &executionFenceStore{}, deployer: deployerMock, portainerClient: executionTestClient{PortainerClient: server, verify: func(context.Context, int, int, string, edge.ExecutionGrant) error {
		if checks.Add(1) > 1 {
			return errors.New("revoked_fence")
		}
		return nil
	}}}
	stack := executionTestStack()
	manager.deployStack(t.Context(), stack, "edge_managed", "stack.yml")
	require.Equal(t, StatusError, stack.Status)
	require.GreaterOrEqual(t, checks.Load(), int32(2))
}

func TestManagedExecutionCancellationReachesDockerCli(t *testing.T) {
	directory := t.TempDir()
	previous := dockerconfig.Dir()
	t.Setenv("DOCKER_CONFIG", directory)
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	dockerconfig.SetDir(directory)
	t.Cleanup(func() { dockerconfig.SetDir(previous) })
	var checks atomic.Int32
	manager := &StackManager{engineType: EngineTypeDockerStandalone, executionFences: &executionFenceStore{}, portainerClient: executionTestClient{verify: func(context.Context, int, int, string, edge.ExecutionGrant) error {
		if checks.Add(1) > 1 {
			return errors.New("revoked_fence")
		}
		return nil
	}}}
	ctx, complete, err := manager.managedExecutionContext(t.Context(), executionTestStack(), "deploy")
	require.NoError(t, err)
	// Initialize the real Docker CLI, without making an engine request. This
	// catches a context reset below the mocked-deployer boundary.
	err = libstack.WithCli(ctx, libstack.DockerCliOptions{Host: "tcp://127.0.0.1:1"}, func(actual context.Context, _ *command.DockerCli) error {
		select {
		case <-actual.Done():
			require.ErrorContains(t, context.Cause(actual), "revoked_fence")
			return actual.Err()
		case <-time.After(5 * time.Second):
			t.Error("actual Docker CLI callback lost managed cancellation")
			return errors.New("cancellation_timeout")
		}
	})
	err = complete(err)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "revoked_fence")
	require.GreaterOrEqual(t, checks.Load(), int32(2))
}

func TestUnreadableFenceStateCannotBecomeUnmanaged(t *testing.T) {
	for _, data := range []string{"incomplete", "null", "{}"} {
		path := filepath.Join(t.TempDir(), "fences.json")
		require.NoError(t, os.WriteFile(path, []byte(data), 0600))
		manager := &StackManager{executionFences: &executionFenceStore{path: path}}
		require.Error(t, manager.verifyManagedExecution(t.Context(), &edgeStack{StackPayload: edge.StackPayload{ID: 4}}, "deploy"))
	}
}
