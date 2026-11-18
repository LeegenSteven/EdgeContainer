package edgestackbusiness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/docker/docker/api/types/container"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/dataservices"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/api/filesystem"
	"github.com/portainer/portainer/api/internal/edge"
	edgestackservice "github.com/portainer/portainer/api/internal/edge/edgestacks"
	"github.com/portainer/portainer/api/stacks/stackutils"
	"go.yaml.in/yaml/v3"
)

type deliveryTemplate struct {
	ServiceID    string   `json:"serviceId"`
	Compose      string   `json:"compose"`
	ModelEnvKeys []string `json:"modelEnvKeys"`
}
type deliveryProvisioner struct {
	store            dataservices.DataStore
	files            portainer.FileService
	stacks           *edgestackservice.Service
	templates        map[string]deliveryTemplate
	PrepareExecution func(context.Context, string, edgestack.Task, int, string) error
	CheckExecution   func(context.Context, int) error
	mounts           *mountProvider
}
type provisionTarget struct {
	GroupID        portainer.EdgeGroupID `json:"groupId"`
	Compose        string                `json:"compose"`
	TemplateDigest string                `json:"templateDigest"`
}

func newDeliveryProvisioner(store dataservices.DataStore, files portainer.FileService, stacks *edgestackservice.Service, name string) (edgestack.DeliveryProvisioner, error) {
	if name == "" {
		return nil, nil
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, errors.New("delivery_templates_unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("delivery_templates_must_be_private")
	}
	templates := map[string]deliveryTemplate{}
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&templates); err != nil {
		return nil, errors.New("delivery_templates_invalid")
	}
	for id, t := range templates {
		if id == "" || t.ServiceID == "" {
			return nil, errors.New("delivery_template_identity_required")
		}
		if _, err := serviceNode([]byte(t.Compose), t.ServiceID); err != nil {
			return nil, err
		}
	}
	return &deliveryProvisioner{store: store, files: files, stacks: stacks, templates: templates}, nil
}
func (p *deliveryProvisioner) target(spec edgestack.DeliveryProvisionSpec) (provisionTarget, error) {
	t, ok := p.templates[spec.Recipe.TemplateID]
	if !ok {
		return provisionTarget{}, errors.New("delivery_template_not_installed")
	}
	root, err := serviceNode([]byte(t.Compose), t.ServiceID)
	if err != nil {
		return provisionTarget{}, err
	}
	service := mappingValue(mappingValue(root.Content[0], "services"), t.ServiceID)
	image := mappingValue(service, "image")
	if image == nil {
		return provisionTarget{}, errors.New("delivery_template_image_required")
	}
	image.Value = spec.Package.Image
	image.Tag = "!!str"
	// Reuse the same approved environment key validation as model replacement.
	task := edgestack.Task{Target: edgestack.Target{ServiceID: t.ServiceID}, ModelEnvKeys: t.ModelEnvKeys}
	content, err := yaml.Marshal(root)
	if err != nil {
		return provisionTarget{}, err
	}
	content, err = patchCompose(content, task, spec.Package)
	if err != nil {
		return provisionTarget{}, err
	}
	var groupID portainer.EdgeGroupID
	err = p.store.ViewTx(func(tx dataservices.DataStoreTx) error {
		endpoint, e := tx.Endpoint().Endpoint(portainer.EndpointID(spec.Device.EndpointID))
		if e != nil {
			return e
		}
		if endpoint.Type != portainer.EdgeAgentOnDockerEnvironment {
			return errors.New("delivery_requires_docker_edge")
		}
		relations, e := edge.FetchEndpointRelationsConfig(tx)
		if e != nil {
			return e
		}
		for _, g := range relations.EdgeGroups {
			endpoints, e := edge.EdgeStackRelatedEndpoints([]portainer.EdgeGroupID{g.ID}, relations.Endpoints, relations.EndpointGroups, relations.EdgeGroups)
			if e == nil && len(endpoints) == 1 && int(endpoints[0]) == spec.Device.EndpointID {
				groupID = g.ID
				break
			}
		}
		if groupID == 0 {
			return errors.New("delivery_independent_device_group_required")
		}
		return nil
	})
	if err != nil {
		return provisionTarget{}, err
	}
	sum := sha256.Sum256([]byte(t.Compose))
	return provisionTarget{GroupID: groupID, Compose: string(content), TemplateDigest: hex.EncodeToString(sum[:])}, nil
}
func (p *deliveryProvisioner) Preview(ctx context.Context, spec edgestack.DeliveryProvisionSpec) (edgestack.DeliveryProvisionPreview, error) {
	target, err := p.target(spec)
	if err != nil {
		return edgestack.DeliveryProvisionPreview{}, err
	}
	if p.CheckExecution == nil {
		return edgestack.DeliveryProvisionPreview{}, errors.New("delivery_managed_execution_required")
	}
	if err := p.CheckExecution(ctx, spec.Device.EndpointID); err != nil {
		return edgestack.DeliveryProvisionPreview{}, err
	}
	probeSpec := spec
	probeSpec.ObserverSourceID = "delivery-preflight"
	probeSpec.ObserverToken = "delivery-preflight"
	if _, err := deliveryObserverCompose(target.Compose, p.templates[spec.Recipe.TemplateID].ServiceID, probeSpec, os.Getenv("EDGESTACK_DELIVERY_OBSERVER_URL")); err != nil {
		return edgestack.DeliveryProvisionPreview{}, err
	}
	if err := stackutils.ValidateEdgeStackComposeContent(ctx, portainer.EdgeStackDeploymentCompose, []byte(target.Compose)); err != nil {
		return edgestack.DeliveryProvisionPreview{}, err
	}
	data, _ := json.Marshal(target)
	sum := sha256.Sum256(data)
	return edgestack.DeliveryProvisionPreview{TargetVersion: hex.EncodeToString(sum[:]), Effects: []string{"创建独立的单设备业务服务。", "使用已发布模型的固定镜像；保留新任务的数据卷。"}}, nil
}
func (p *deliveryProvisioner) Apply(ctx context.Context, spec edgestack.DeliveryProvisionSpec, preview edgestack.DeliveryProvisionPreview, operationID string) (edgestack.Task, error) {
	current, err := p.Preview(ctx, spec)
	if err != nil {
		return edgestack.Task{}, err
	}
	if current.TargetVersion != preview.TargetVersion {
		return edgestack.Task{}, errors.New("delivery_template_or_target_changed")
	}
	target, err := p.target(spec)
	if err != nil {
		return edgestack.Task{}, err
	}
	if operationID == "" || spec.TaskID == "" || strings.ContainsAny(spec.TaskID, "/\\\x00") {
		return edgestack.Task{}, errors.New("delivery_operation_required")
	}
	name := "edgestack-delivery-" + spec.TaskID
	target.Compose, err = deliveryObserverCompose(target.Compose, p.templates[spec.Recipe.TemplateID].ServiceID, spec, os.Getenv("EDGESTACK_DELIVERY_OBSERVER_URL"))
	if err != nil {
		return edgestack.Task{}, err
	}
	var stackID int
	err = p.store.UpdateTx(func(tx dataservices.DataStoreTx) error {
		rows, err := tx.EdgeStack().EdgeStacks()
		if err != nil {
			return err
		}
		for _, stack := range rows {
			if stack.Name != name {
				continue
			}
			if len(stack.EdgeGroups) != 1 || stack.EdgeGroups[0] != target.GroupID {
				return errors.New("delivery_existing_target_mismatch")
			}
			content, err := p.files.GetFileContent(stack.ProjectPath, stack.EntryPoint)
			if err != nil {
				return err
			}
			if !bytes.Equal(content, []byte(target.Compose)) {
				return errors.New("delivery_existing_compose_mismatch")
			}
			stackID = int(stack.ID)
			return p.prepare(ctx, spec, operationID, stackID, stack.Version)
		}
		stack, err := p.stacks.BuildEdgeStack(tx, name, portainer.EdgeStackDeploymentCompose, []portainer.EdgeGroupID{target.GroupID}, nil, false)
		if err != nil {
			return err
		}
		if err := p.prepare(ctx, spec, operationID, int(stack.ID), stack.Version); err != nil {
			return err
		}
		_, err = p.stacks.PersistEdgeStack(tx, stack, func(folder string, _ []portainer.EndpointID) (string, string, string, error) {
			project, err := p.files.StoreEdgeStackFileFromBytes(folder, filesystem.ComposeFileDefaultName, []byte(target.Compose))
			return filesystem.ComposeFileDefaultName, "", project, err
		})
		stackID = int(stack.ID)
		return err
	})
	if err != nil {
		return edgestack.Task{}, err
	}
	t := p.templates[spec.Recipe.TemplateID]
	return edgestack.Task{ID: spec.TaskID, Name: spec.Name, DeviceID: spec.Device.ID, Kind: spec.Recipe.TaskKind, RuntimeBackend: spec.Recipe.RuntimeBackend, TemplateID: spec.Recipe.TemplateID, Revision: 1, State: "registered",
		Target: edgestack.Target{EndpointID: spec.Device.EndpointID, StackID: stackID, StackName: name, ServiceID: t.ServiceID},
		ACL:    spec.ACL, Models: []edgestack.ModelBinding{{Slot: spec.Package.Slot, PackageID: spec.Package.ID, Name: spec.Package.Name, Version: spec.Package.Version, State: "unobserved"}},
		ModelEnvKeys: t.ModelEnvKeys, Capabilities: []string{"replace-model"}}, nil
}

// A new-task rollback stops only the exact task-owned service, preserving volumes.
func (p *deliveryProvisioner) prepare(ctx context.Context, spec edgestack.DeliveryProvisionSpec, operationID string, stackID, version int) error {
	if p.PrepareExecution == nil {
		return errors.New("delivery_managed_execution_required")
	}
	return p.PrepareExecution(ctx, operationID, edgestack.Task{ID: spec.TaskID, DeviceID: spec.Device.ID, Target: edgestack.Target{EndpointID: spec.Device.EndpointID, StackID: stackID, StackName: "edgestack-delivery-" + spec.TaskID, ServiceID: p.templates[spec.Recipe.TemplateID].ServiceID}}, version, "deploy")
}
func (p *deliveryProvisioner) Rollback(ctx context.Context, spec edgestack.DeliveryProvisionSpec, operationID string) error {
	if operationID == "" || spec.TaskID == "" || p.mounts == nil {
		return errors.New("delivery_rollback_unavailable")
	}
	name := "edgestack-delivery-" + spec.TaskID
	task := edgestack.Task{ID: spec.TaskID, DeviceID: spec.Device.ID, Target: edgestack.Target{EndpointID: spec.Device.EndpointID, StackName: name, ServiceID: p.templates[spec.Recipe.TemplateID].ServiceID}}
	err := p.store.UpdateTx(func(tx dataservices.DataStoreTx) error {
		rows, err := tx.EdgeStack().EdgeStacks()
		if err != nil {
			return err
		}
		for _, stack := range rows {
			if stack.Name != name {
				continue
			}
			task.Target.StackID = int(stack.ID)
			raw, err := p.files.GetFileContent(stack.ProjectPath, stack.EntryPoint)
			if err != nil {
				return err
			}
			root, err := serviceNode(raw, task.Target.ServiceID)
			if err != nil {
				return err
			}
			service := mappingValue(mappingValue(root.Content[0], "services"), task.Target.ServiceID)
			image := mappingValue(service, "image")
			if image == nil || image.Value != spec.Package.Image {
				return errors.New("delivery_rollback_target_changed")
			}
			deploy := mappingValue(service, "deploy")
			if deploy == nil {
				deploy = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				service.Content = append(service.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "deploy", Tag: "!!str"}, deploy)
			}
			replicas := mappingValue(deploy, "replicas")
			version := stack.Version
			alreadyStopped := replicas != nil && replicas.Value == "0"
			if !alreadyStopped {
				version++
			}
			if err := p.prepare(ctx, spec, operationID, int(stack.ID), version); err != nil {
				return err
			}
			if alreadyStopped {
				return nil
			}
			if replicas == nil {
				deploy.Content = append(deploy.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "replicas", Tag: "!!str"}, &yaml.Node{Kind: yaml.ScalarNode, Value: "0", Tag: "!!int"})
			} else {
				replicas.Value = "0"
				replicas.Tag = "!!int"
			}
			content, err := yaml.Marshal(root)
			if err != nil {
				return err
			}
			if _, err := p.files.StoreEdgeStackFileFromBytes(strconv.Itoa(int(stack.ID)), stack.EntryPoint, content); err != nil {
				return err
			}
			stack.Version = version
			if err := tx.EdgeStackStatus().Clear(stack.ID, []portainer.EndpointID{portainer.EndpointID(spec.Device.EndpointID)}); err != nil {
				return err
			}
			return tx.EdgeStack().UpdateEdgeStack(stack.ID, &stack)
		}
		return errors.New("delivery_rollback_task_missing")
	})
	if err != nil {
		return err
	}
	// A desired replica count is not evidence that the Edge executor has stopped.
	// Return uncertainty until Docker proves the owned service has no running container.
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	stoppedReads := 0
	identity, err := p.mounts.containerTarget(task)
	if err != nil {
		return err
	}
	for {
		client, err := p.mounts.newClient(ctx, task)
		if err == nil && client != nil {
			rows, e := client.ContainerList(ctx, container.ListOptions{All: true, Filters: identity.filters()})
			stopped := false
			if e == nil {
				stopped, e = inspectExecutionContainersStopped(ctx, client, rows, identity)
			}
			client.Close()
			if e == nil && stopped {
				stoppedReads++
				status, statusErr := p.store.EdgeStackStatus().Read(portainer.EdgeStackID(task.Target.StackID), portainer.EndpointID(task.Target.EndpointID))
				if stoppedReads >= 2 && statusErr == nil {
					for _, state := range status.Status {
						if state.Type == portainer.EdgeStackStatusAcknowledged {
							return nil
						}
					}
				}
			} else {
				stoppedReads = 0
			}
		} else {
			stoppedReads = 0
		}
		select {
		case <-ctx.Done():
			return errors.New("delivery_rollback_stop_unconfirmed")
		case <-tick.C:
		}
	}
}

// Paused/restarting/created containers still occupy the target. An empty or
// exited-only result is safe only after the new desired version is acknowledged.
func deliveryContainersStopped(rows []container.Summary, target edgestack.Target) bool {
	identity, err := containerTarget("", target)
	return err == nil && executionContainersStopped(rows, identity)
}

func executionContainersStopped(rows []container.Summary, identity executionContainerTarget) bool {
	for _, row := range rows {
		if !identity.matches(row.Labels) {
			return false
		}
		if row.State != "exited" && row.State != "dead" {
			return false
		}
	}
	return true
}

// Scoped observer credentials travel as an inline Compose config copied into
// the runtime container, never an image layer or an environment value.
func deliveryObserverCompose(compose, serviceID string, spec edgestack.DeliveryProvisionSpec, serverURL string) (string, error) {
	if spec.ObserverSourceID == "" || spec.ObserverToken == "" {
		return "", errors.New("delivery_observer_credential_required")
	}
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("delivery_observer_url_required")
	}
	root, err := serviceNode([]byte(compose), serviceID)
	if err != nil {
		return "", err
	}
	service := mappingValue(mappingValue(root.Content[0], "services"), serviceID)
	if mappingValue(service, "healthcheck") == nil {
		return "", errors.New("delivery_healthcheck_required")
	}
	// Compose copies inline configs into the container root filesystem. It
	// cannot perform that operation with read_only=true. Refuse inheritance or
	// unresolved values rather than overlooking a read-only setting from a merge.
	if service.Anchor != "" || mappingValue(service, "<<") != nil {
		return "", errors.New("delivery_observer_service_inheritance_unsupported")
	}
	if readOnly := mappingValue(service, "read_only"); readOnly != nil {
		var value bool
		if readOnly.Kind != yaml.ScalarNode || readOnly.Tag != "!!bool" || readOnly.Decode(&value) != nil || value {
			return "", errors.New("delivery_observer_writable_root_required")
		}
	}
	const configName = "edgestack-model-observer"
	const tokenTarget = "/run/secrets/edgestack-model-observer"
	for _, kind := range []string{"configs", "secrets"} {
		if err := deliveryObserverFileReferences(mappingValue(service, kind), kind, configName, tokenTarget); err != nil {
			return "", err
		}
	}
	setMapping := func(parent *yaml.Node, key string, value *yaml.Node) {
		for i := 0; i+1 < len(parent.Content); i += 2 {
			if parent.Content[i].Value == key {
				parent.Content[i+1] = value
				return
			}
		}
		parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	}
	scalar := func(value string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value} }
	env := mappingValue(service, "environment")
	if env == nil {
		env = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMapping(service, "environment", env)
	}
	values := map[string]string{"EDGESTACK_MODEL_OBSERVER_SOURCE_ID": spec.ObserverSourceID, "EDGESTACK_MODEL_OBSERVER_TOKEN_FILE": tokenTarget, "EDGESTACK_SERVER_URL": strings.TrimRight(serverURL, "/"), "EDGESTACK_MODEL_OBSERVER_TASK_ID": spec.TaskID, "EDGESTACK_MODEL_OBSERVER_DEVICE_ID": spec.Device.ID, "EDGESTACK_MODEL_OBSERVER_SLOT": spec.Package.Slot}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := values[key]
		if env.Kind == yaml.MappingNode {
			setMapping(env, key, scalar(value))
		} else if env.Kind == yaml.SequenceNode {
			for _, node := range env.Content {
				if strings.HasPrefix(node.Value, key+"=") {
					return "", errors.New("delivery_observer_environment_conflict")
				}
			}
			env.Content = append(env.Content, scalar(key+"="+value))
		} else {
			return "", errors.New("delivery_observer_environment_invalid")
		}
	}
	configs := mappingValue(root.Content[0], "configs")
	if configs == nil {
		configs = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMapping(root.Content[0], "configs", configs)
	}
	if configs.Kind != yaml.MappingNode || configs.Anchor != "" || mappingValue(configs, "<<") != nil {
		return "", errors.New("delivery_observer_config_invalid")
	}
	if mappingValue(configs, configName) != nil {
		return "", errors.New("delivery_observer_config_conflict")
	}
	config := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setMapping(config, "content", scalar(strings.ReplaceAll(spec.ObserverToken, "$", "$$")))
	setMapping(configs, configName, config)
	mounts := mappingValue(service, "configs")
	if mounts == nil {
		mounts = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		setMapping(service, "configs", mounts)
	}
	if mounts.Kind != yaml.SequenceNode {
		return "", errors.New("delivery_observer_config_mount_invalid")
	}
	reference := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setMapping(reference, "source", scalar(configName))
	setMapping(reference, "target", scalar(tokenTarget))
	setMapping(reference, "uid", scalar("0"))
	setMapping(reference, "gid", scalar("0"))
	setMapping(reference, "mode", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(0o444)})
	mounts.Content = append(mounts.Content, reference)
	raw, err := yaml.Marshal(root)
	return string(raw), err
}

// Do not overwrite a template's foreign config/secret or shadow its target.
func deliveryObserverFileReferences(refs *yaml.Node, kind, name, target string) error {
	if refs == nil {
		return nil
	}
	if refs.Kind != yaml.SequenceNode {
		return errors.New("delivery_observer_config_mount_invalid")
	}
	for _, ref := range refs.Content {
		var source, destination string
		switch ref.Kind {
		case yaml.ScalarNode:
			source = ref.Value
		case yaml.MappingNode:
			if ref.Anchor != "" || mappingValue(ref, "<<") != nil {
				return errors.New("delivery_observer_config_mount_invalid")
			}
			src := mappingValue(ref, "source")
			if src == nil || src.Kind != yaml.ScalarNode {
				return errors.New("delivery_observer_config_mount_invalid")
			}
			source = src.Value
			if dst := mappingValue(ref, "target"); dst != nil {
				if dst.Kind != yaml.ScalarNode {
					return errors.New("delivery_observer_config_mount_invalid")
				}
				destination = dst.Value
			}
		default:
			return errors.New("delivery_observer_config_mount_invalid")
		}
		if destination == "" {
			destination = "/" + source
			if kind == "secrets" {
				destination = "/run/secrets/" + source
			}
		} else if !path.IsAbs(destination) && kind == "secrets" {
			destination = "/run/secrets/" + destination
		}
		destination = path.Clean("/" + destination)
		if source == name || destination == target || strings.HasPrefix(target, strings.TrimSuffix(destination, "/")+"/") {
			return errors.New("delivery_observer_config_conflict")
		}
	}
	return nil
}
