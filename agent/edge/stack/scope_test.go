package stack

import (
	"path/filepath"
	"testing"

	"github.com/portainer/agent"
	"github.com/portainer/agent/edge/client"
	"github.com/portainer/agent/internals/mocks"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edge"
	"github.com/portainer/portainer/pkg/libstack"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestScopedManagerDoesNotAdoptOrDeleteForeignStacks(t *testing.T) {
	ctrl := gomock.NewController(t)
	deployerMock := mocks.NewMockDeployer(ctrl)
	server := mocks.NewMockPortainerClient(ctrl)
	manager := NewStackManager(server, nil, "acceptance-edge", nil)
	require.NoError(t, manager.SetStackScope("blue"))
	manager.deployer = deployerMock
	manager.isEnabled = true
	manager.executionFences = &executionFenceStore{path: filepath.Join(t.TempDir(), "fences.json")}
	// Foreign entries deliberately have the same ID and are returned both before
	// and after our entry: filtering must happen before numeric ID deduplication.
	deployerMock.EXPECT().GetEdgeStacks(gomock.Any()).Return([]agent.EdgeStack{
		{ID: 1, Name: "production-voice"},
		{ID: 1, Name: "vision", OwnerScope: "blue"},
		{ID: 1, Name: "other-vision", OwnerScope: "green"},
		{ID: 2, Name: "production-flowcheck"},
	}, nil)
	require.NoError(t, manager.LoadExistingEdgeStacks(t.Context()))
	require.Len(t, manager.stacks, 1)
	require.Equal(t, "vision", manager.stacks[1].Name)
	require.Contains(t, manager.stacks[1].FileFolder, "/scopes/blue/1")
	// Empty polling may reconcile only our own discovered project. Neither
	// foreign stack can enter the deletion queue, even with the same stack ID.
	require.NoError(t, manager.UpdateStacksStatus(map[int]client.StackStatus{}))
	deployerMock.EXPECT().Remove(gomock.Any(), "edge_blue_vision", gomock.Any(), gomock.Any()).Return(nil)
	server.EXPECT().SetEdgeStackStatus(1, 0, portainer.EdgeStackStatusRemoving, gomock.Any(), "").Return(nil)
	manager.performActionOnStack()
	require.Equal(t, StatusAwaitingRemovedStatus, manager.stacks[1].Status)
	require.ErrorContains(t, manager.DeleteNormalStack(t.Context(), "edge_production-voice", true), "unavailable")
}

func TestScopedManagerKeepsSeparateFilesAndRejectsUnsupportedEngines(t *testing.T) {
	stack := &edgeStack{StackPayload: edge.StackPayload{ID: 1}}
	require.NotEqual(t, getStackFileFolder(stack, "blue"), getStackFileFolder(stack, "green"))
	require.NotEqual(t, getStackFileFolder(stack), getStackFileFolder(stack, "blue"))
	manager := NewStackManager(nil, nil, "edge", nil)
	require.Error(t, manager.SetStackScope("../blue"))
	require.NoError(t, manager.SetStackScope("blue"))
	require.NoError(t, manager.SetEngineType(EngineTypeDockerStandalone))
	require.ErrorContains(t, manager.SetEngineType(EngineTypeDockerSwarm), "require Docker standalone")
	require.ErrorContains(t, manager.SetStackScope("green"), "after initialization")
	require.Equal(t, "edge_blue_vision", libstack.EdgeStackProjectName(manager.stackScope, "vision"))
}
