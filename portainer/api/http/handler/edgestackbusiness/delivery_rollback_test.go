package edgestackbusiness

import (
	"context"
	"testing"
	"time"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/api/filesystem"
	"github.com/stretchr/testify/require"
)

func TestDeliveryRollbackPreservesPreviousFileOnWriteFailure(t *testing.T) {
	for _, failure := range []string{"status", "commit"} {
		t.Run(failure, func(t *testing.T) {
			_, store, files := dispatcherFixture(t)
			before := stoppedReplicaCompose("    deploy: {replicas: 3}\n    scale: 3\n")
			project, err := files.StoreEdgeStackFileFromBytes("9", filesystem.ComposeFileDefaultName, []byte(before))
			require.NoError(t, err)
			stack := &portainer.EdgeStack{ID: 9, Name: "edgestack-delivery-test", Version: 1, ProjectPath: project, EntryPoint: filesystem.ComposeFileDefaultName, DeploymentType: portainer.EdgeStackDeploymentCompose, EdgeGroups: []portainer.EdgeGroupID{1}}
			require.NoError(t, store.EdgeStack().Create(9, stack))
			spec := edgestack.DeliveryProvisionSpec{TaskID: "test", Recipe: edgestack.DeliveryRecipe{TemplateID: "vision"}, Device: edgestack.Device{ID: "board", EndpointID: 4}, Package: edgestack.ModelPackage{Image: "example/old:1"}}
			p := &deliveryProvisioner{files: files, templates: map[string]deliveryTemplate{"vision": {ServiceID: "voice"}}, mounts: &mountProvider{}}
			p.PrepareExecution = func(_ context.Context, op string, task edgestack.Task, version int, _ string) error {
				require.Equal(t, "rollback-test", op)
				require.Equal(t, 9, task.Target.StackID)
				require.Equal(t, 2, version)
				return nil
			}
			if failure == "status" {
				p.store = failStatusStore{store}
			} else {
				p.store = &failCommitStore{DataStore: store, once: true}
			}
			err = p.Rollback(t.Context(), spec, "rollback-test")
			require.ErrorIs(t, err, errInjectedDeployment)
			content, err := files.GetFileContent(project, stack.EntryPoint)
			require.NoError(t, err)
			require.Equal(t, before, string(content))
			stored, err := store.EdgeStack().EdgeStack(9)
			require.NoError(t, err)
			require.Equal(t, 1, stored.Version)
			if failure == "commit" {
				return
			}

			// Repeat the operation after the failure and again after success.
			p.store = store
			client := &executionStopClient{}
			p.mounts.newClient = func(context.Context, edgestack.Task) (mountClient, error) {
				return &changingHandoffClient{client, func() {
					require.NoError(t, store.EdgeStackStatus().Update(9, 4, &portainer.EdgeStackStatusForEnv{EndpointID: 4, Status: []portainer.EdgeStackDeploymentStatus{{Version: 2, Type: portainer.EdgeStackStatusCompleted}}}))
				}}, nil
			}
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			for range 2 {
				require.NoError(t, p.Rollback(ctx, spec, "rollback-test"))
				stored, err := store.EdgeStack().EdgeStack(9)
				require.NoError(t, err)
				require.Equal(t, 2, stored.Version)
				content, err := files.GetFileContent(project, stack.EntryPoint)
				require.NoError(t, err)
				root, err := serviceNode(content, "voice")
				require.NoError(t, err)
				svc := mappingValue(mappingValue(root.Content[0], "services"), "voice")
				require.Equal(t, "0", mappingValue(svc, "scale").Value)
				require.Equal(t, "0", mappingValue(mappingValue(svc, "deploy"), "replicas").Value)
				require.Contains(t, string(content), "/voice-data:/data")
			}
			require.EqualValues(t, 4, client.lists.Load())
		})
	}
}
