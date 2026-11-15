package edgestackbusiness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/dataservices"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/api/internal/edge"
	"github.com/portainer/portainer/api/stacks/stackutils"
	"go.yaml.in/yaml/v3"
)

// Dispatcher uses the existing Edge Stack datastore/file/status deployment
// mechanism. It supports only independent, single-service Docker Compose Edge
// Stacks targeting one approved endpoint; other resources remain unavailable.
type Dispatcher struct {
	store dataservices.DataStore
	files portainer.FileService
}

func (d *Dispatcher) target(tx dataservices.DataStoreTx, task edgestack.Task) (*portainer.EdgeStack, []byte, error) {
	settings, err := tx.Settings().Settings()
	if err != nil || !settings.EnableEdgeComputeFeatures {
		return nil, nil, errors.New("edge compute disabled")
	}
	stack, err := tx.EdgeStack().EdgeStack(portainer.EdgeStackID(task.Target.StackID))
	if err != nil {
		return nil, nil, err
	}
	if stack.DeploymentType != portainer.EdgeStackDeploymentCompose {
		return nil, nil, errors.New("only compose supported")
	}
	config, err := edge.FetchEndpointRelationsConfig(tx)
	if err != nil {
		return nil, nil, err
	}
	endpoints, err := edge.EdgeStackRelatedEndpoints(stack.EdgeGroups, config.Endpoints, config.EndpointGroups, config.EdgeGroups)
	if err != nil {
		return nil, nil, err
	}
	if len(endpoints) != 1 || int(endpoints[0]) != task.Target.EndpointID {
		return nil, nil, errors.New("task requires an independent single-device stack")
	}
	endpoint, err := tx.Endpoint().Endpoint(endpoints[0])
	if err != nil {
		return nil, nil, err
	}
	if endpoint.Type != portainer.EdgeAgentOnDockerEnvironment {
		return nil, nil, errors.New("only Docker Edge endpoints supported")
	}
	content, err := d.files.GetFileContent(stack.ProjectPath, stack.EntryPoint)
	return stack, content, err
}
func version(stack *portainer.EdgeStack, content []byte) string {
	sum := sha256.Sum256(content)
	return strconv.Itoa(stack.Version) + ":" + hex.EncodeToString(sum[:])
}
func (d *Dispatcher) ValidateTask(ctx context.Context, task edgestack.Task) error {
	return d.store.ViewTx(func(tx dataservices.DataStoreTx) error {
		_, content, err := d.target(tx, task)
		if err != nil {
			return err
		}
		_, err = serviceNode(content, task.Target.ServiceID)
		return err
	})
}
func (d *Dispatcher) Preview(ctx context.Context, task edgestack.Task, model edgestack.ModelPackage) (string, error) {
	var token string
	err := d.store.ViewTx(func(tx dataservices.DataStoreTx) error {
		stack, content, err := d.target(tx, task)
		if err != nil {
			return err
		}
		if _, err = patchCompose(content, task, model); err != nil {
			return err
		}
		token = version(stack, content)
		return nil
	})
	return token, err
}
func (d *Dispatcher) Apply(ctx context.Context, task edgestack.Task, model edgestack.ModelPackage, expected string) error {
	var before, updated []byte
	var original *portainer.EdgeStack
	fileTouched, restored := false, false
	err := d.store.UpdateTx(func(tx dataservices.DataStoreTx) (txErr error) {
		stack, content, err := d.target(tx, task)
		if err != nil {
			return err
		}
		if expected != version(stack, content) {
			return errors.New("target changed after preview")
		}
		updated, err = patchCompose(content, task, model)
		if err != nil {
			return err
		}
		if err := stackutils.ValidateEdgeStackComposeContent(ctx, stack.DeploymentType, updated); err != nil {
			return err
		}
		copyOfStack := *stack
		original = &copyOfStack
		before = append([]byte(nil), content...)
		// Filesystem writes are outside bbolt's transaction. Restore the
		// exact prior bytes before releasing the write lock when any later
		// status/metadata write fails (including a partial file-write error).
		defer func() {
			if txErr != nil && fileTouched {
				restored = true
				if _, restoreErr := d.files.StoreEdgeStackFileFromBytes(strconv.Itoa(int(original.ID)), original.EntryPoint, before); restoreErr != nil {
					txErr = errors.Join(txErr, fmt.Errorf("restore previous compose: %w", restoreErr))
				}
			}
		}()
		// This is the same version bump, file store and status clearing used by
		// the existing administrator Edge Stack update handler.
		fileTouched = true
		if _, err := d.files.StoreEdgeStackFileFromBytes(strconv.Itoa(int(stack.ID)), stack.EntryPoint, updated); err != nil {
			return err
		}
		stack.Version++
		if err := tx.EdgeStackStatus().Clear(stack.ID, []portainer.EndpointID{portainer.EndpointID(task.Target.EndpointID)}); err != nil {
			return err
		}
		return tx.EdgeStack().UpdateEdgeStack(stack.ID, stack)
	})
	if err != nil && fileTouched && !restored {
		// A transaction commit can fail after the callback succeeded. Reacquire
		// the datastore lock, and restore only if the old DB version and our
		// exact emitted file still match; never overwrite an intervening edit.
		restoreErr := d.store.UpdateTx(func(tx dataservices.DataStoreTx) error {
			current, readErr := tx.EdgeStack().EdgeStack(original.ID)
			if readErr != nil {
				return readErr
			}
			if current.Version != original.Version || current.EntryPoint != original.EntryPoint || current.ProjectPath != original.ProjectPath {
				return errors.New("target changed before compose restoration")
			}
			currentContent, readErr := d.files.GetFileContent(current.ProjectPath, current.EntryPoint)
			if readErr != nil {
				return readErr
			}
			if bytes.Equal(currentContent, before) {
				return nil
			}
			if !bytes.Equal(currentContent, updated) {
				return errors.New("compose changed before restoration")
			}
			_, restoreErr := d.files.StoreEdgeStackFileFromBytes(strconv.Itoa(int(original.ID)), original.EntryPoint, before)
			return restoreErr
		})
		if restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore previous compose after commit failure: %w", restoreErr))
		}
	}
	return err
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
func document(content []byte) (*yaml.Node, error) {
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	if len(root.Content) != 1 {
		return nil, errors.New("invalid compose")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("multiple compose documents not supported")
	}
	return &root, nil
}
func serviceNode(content []byte, serviceID string) (*yaml.Node, error) {
	root, err := document(content)
	if err != nil {
		return nil, err
	}
	services := mappingValue(root.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode || len(services.Content) != 2 || services.Content[0].Value != serviceID || services.Content[1].Kind != yaml.MappingNode {
		return nil, errors.New("task requires one independent service")
	}
	return root, nil
}
func patchCompose(content []byte, task edgestack.Task, model edgestack.ModelPackage) ([]byte, error) {
	root, err := serviceNode(content, task.Target.ServiceID)
	if err != nil {
		return nil, err
	}
	services := mappingValue(root.Content[0], "services")
	service := services.Content[1]
	image := mappingValue(service, "image")
	if image == nil || image.Kind != yaml.ScalarNode {
		return nil, errors.New("explicit image required")
	}
	image.Value = model.Image
	image.Tag = "!!str"
	env := mappingValue(service, "environment")
	for key, value := range model.Environment {
		allowed := false
		for _, k := range task.ModelEnvKeys {
			if k == key {
				allowed = true
			}
		}
		if !allowed {
			return nil, errors.New("model environment key not approved")
		}
		found := false
		if env != nil && env.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(env.Content); i += 2 {
				if env.Content[i].Value == key {
					env.Content[i+1].Value = value
					env.Content[i+1].Kind = yaml.ScalarNode
					env.Content[i+1].Tag = "!!str"
					env.Content[i+1].Content = nil
					found = true
				}
			}
		} else if env != nil && env.Kind == yaml.SequenceNode {
			for _, node := range env.Content {
				if node.Kind != yaml.ScalarNode {
					return nil, errors.New("unsupported environment")
				}
				parts := strings.SplitN(node.Value, "=", 2)
				if parts[0] == key {
					node.Value = key + "=" + value
					node.Tag = "!!str"
					found = true
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("approved model environment key %s is absent", key)
		}
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
