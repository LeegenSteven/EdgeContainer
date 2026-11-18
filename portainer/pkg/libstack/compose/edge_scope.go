package compose

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/cli/cli/command"
	"github.com/docker/compose/v2/pkg/api"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/pkg/libstack"
)

// Scoped stacks deliberately omit the legacy stack ID label: an older Agent
// sharing this Docker daemon must not discover and adopt these projects.
func NewScopedComposeDeployer(scope string) (*ComposeDeployer, error) {
	if err := libstack.ValidateEdgeStackScope(scope); err != nil {
		return nil, err
	}
	c := NewComposeDeployer()
	c.ownerScope = scope
	return c, nil
}

func (c *ComposeDeployer) validateProjectScope(name string) error {
	if c.ownerScope != "" && !strings.HasPrefix(name, libstack.EdgeStackProjectName(c.ownerScope, "")) {
		return fmt.Errorf("edge stack project is outside execution scope")
	}
	return nil
}

// Names alone are insufficient proof of ownership. Refuse to up/down an
// existing project if any of its containers is legacy or belongs to another
// scope, including stopped containers and orphans.
func (c *ComposeDeployer) verifyProjectScope(ctx context.Context, service api.Compose, name string) error {
	if c.ownerScope == "" {
		return nil
	}
	if err := c.validateProjectScope(name); err != nil {
		return err
	}
	containers, err := service.Ps(ctx, name, api.PsOptions{All: true})
	if err != nil {
		return fmt.Errorf("unable to verify edge stack scope: %w", err)
	}
	for _, container := range containers {
		_, legacyID := container.Labels[PortainerEdgeStackLabel]
		if container.Labels[libstack.EdgeStackScopeLabel] != c.ownerScope || container.Labels[api.ProjectLabel] != name || legacyID {
			return fmt.Errorf("edge stack project contains a foreign or legacy container")
		}
	}
	return nil
}

type scopeResourceClient interface {
	NetworkList(context.Context, network.ListOptions) ([]network.Summary, error)
	VolumeList(context.Context, volume.ListOptions) (volume.ListResponse, error)
}

func (c *ComposeDeployer) scopeResourceClient(cli command.Cli) scopeResourceClient {
	if c.scopeResourceClientFn != nil {
		return c.scopeResourceClientFn(cli)
	}
	return cli.Client()
}

// Compose may reuse a colliding network/volume after merely logging a warning.
// Check actual resources, including projects with no containers, before any
// mutation and again after Up so that warning cannot become successful delivery.
func (c *ComposeDeployer) verifyResourceScope(ctx context.Context, client scopeResourceClient, name string, project *types.Project, requireExisting bool) error {
	if c.ownerScope == "" {
		return nil
	}
	if err := c.validateProjectScope(name); err != nil {
		return err
	}
	expectedNetworks, expectedVolumes := map[string]bool{}, map[string]bool{}
	if project != nil {
		for _, resource := range project.Networks {
			expectedNetworks[resource.Name] = bool(resource.External)
		}
		for _, resource := range project.Volumes {
			expectedVolumes[resource.Name] = bool(resource.External)
		}
	}
	networks, err := client.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return fmt.Errorf("unable to verify edge stack network scope: %w", err)
	}
	volumes, err := client.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return fmt.Errorf("unable to verify edge stack volume scope: %w", err)
	}
	owned := func(labels map[string]string) bool {
		return labels[libstack.EdgeStackScopeLabel] == c.ownerScope && labels[api.ProjectLabel] == name
	}
	seenNetworks, seenVolumes := map[string]bool{}, map[string]bool{}
	for _, resource := range networks {
		_, expected := expectedNetworks[resource.Name]
		if (expected || resource.Labels[api.ProjectLabel] == name) && !owned(resource.Labels) {
			return fmt.Errorf("edge stack network belongs to a foreign or legacy project")
		}
		if expected {
			seenNetworks[resource.Name] = true
		}
	}
	for _, resource := range volumes.Volumes {
		if resource == nil {
			return fmt.Errorf("invalid edge stack volume scope response")
		}
		_, expected := expectedVolumes[resource.Name]
		if (expected || resource.Labels[api.ProjectLabel] == name) && !owned(resource.Labels) {
			return fmt.Errorf("edge stack volume belongs to a foreign or legacy project")
		}
		if expected {
			seenVolumes[resource.Name] = true
		}
	}
	for resourceName, external := range expectedNetworks {
		if !seenNetworks[resourceName] && (requireExisting || external) {
			return fmt.Errorf("edge stack network ownership unconfirmed")
		}
	}
	for resourceName, external := range expectedVolumes {
		if !seenVolumes[resourceName] && (requireExisting || external) {
			return fmt.Errorf("edge stack volume ownership unconfirmed")
		}
	}
	return nil
}

func (c *ComposeDeployer) labelScopedProject(project *types.Project, stackID portainer.EdgeStackID) error {
	if c.ownerScope == "" {
		return nil
	}
	if err := c.validateProjectScope(project.Name); err != nil {
		return err
	}
	for i, service := range project.Services {
		// A fixed global container name bypasses Compose's project namespace.
		if service.ContainerName != "" {
			return fmt.Errorf("explicit container_name is not supported for scoped edge stacks")
		}
		if service.Labels == nil {
			service.Labels = types.Labels{}
		}
		if service.CustomLabels == nil {
			service.CustomLabels = types.Labels{}
		}
		delete(service.Labels, PortainerEdgeStackLabel)
		delete(service.Labels, libstack.EdgeStackScopedIDLabel)
		delete(service.CustomLabels, PortainerEdgeStackLabel)
		service.Labels[libstack.EdgeStackScopeLabel] = c.ownerScope
		service.CustomLabels[libstack.EdgeStackScopeLabel] = c.ownerScope
		if stackID > 0 {
			service.CustomLabels[libstack.EdgeStackScopedIDLabel] = strconv.Itoa(int(stackID))
		}
		project.Services[i] = service
	}
	// Compose projects have no independent Docker object; label their owned
	// resources as well as services, retaining the scope in the project name.
	for name, network := range project.Networks {
		if network.External {
			continue
		}
		if !strings.HasPrefix(network.Name, project.Name+"_") {
			return fmt.Errorf("network name is outside edge stack project scope")
		}
		if network.Labels == nil {
			network.Labels = types.Labels{}
		}
		network.Labels[libstack.EdgeStackScopeLabel] = c.ownerScope
		project.Networks[name] = network
	}
	for name, volume := range project.Volumes {
		if volume.External {
			continue
		}
		if !strings.HasPrefix(volume.Name, project.Name+"_") {
			return fmt.Errorf("volume name is outside edge stack project scope")
		}
		if volume.Labels == nil {
			volume.Labels = types.Labels{}
		}
		volume.Labels[libstack.EdgeStackScopeLabel] = c.ownerScope
		project.Volumes[name] = volume
	}
	return nil
}
