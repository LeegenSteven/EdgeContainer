package compose

import (
	"context"
	"errors"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/cli/cli/command"
	"github.com/docker/compose/v2/pkg/api"
	"github.com/docker/compose/v2/pkg/compose"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/portainer/portainer/pkg/libstack"
	"github.com/stretchr/testify/require"
)

type scopedComposeTestService struct {
	api.Compose
	containers    map[string][]api.ContainerSummary
	upProjects    []*types.Project
	removed       []string
	networks      []network.Summary
	volumes       []*volume.Volume
	afterUp       func()
	resourceError error
}

func (s *scopedComposeTestService) List(context.Context, api.ListOptions) ([]api.Stack, error) {
	var result []api.Stack
	for name := range s.containers {
		result = append(result, api.Stack{Name: name})
	}
	return result, nil
}
func (s *scopedComposeTestService) Ps(_ context.Context, name string, options api.PsOptions) ([]api.ContainerSummary, error) {
	return s.containers[name], nil
}
func (s *scopedComposeTestService) Build(context.Context, *types.Project, api.BuildOptions) error {
	return nil
}
func (s *scopedComposeTestService) Up(_ context.Context, project *types.Project, _ api.UpOptions) error {
	s.upProjects = append(s.upProjects, project)
	var summaries []api.ContainerSummary
	for _, service := range project.Services {
		labels := make(map[string]string)
		for key, value := range service.Labels {
			labels[key] = value
		}
		for key, value := range service.CustomLabels {
			labels[key] = value
		}
		summaries = append(summaries, api.ContainerSummary{Labels: labels})
	}
	s.containers[project.Name] = summaries
	for _, resource := range project.Networks {
		found := false
		for _, existing := range s.networks {
			if existing.Name == resource.Name {
				found = true
			}
		}
		if !found {
			labels := map[string]string{api.ProjectLabel: project.Name}
			for key, value := range resource.Labels {
				labels[key] = value
			}
			s.networks = append(s.networks, network.Summary{Name: resource.Name, Labels: labels})
		}
	}
	for _, resource := range project.Volumes {
		found := false
		for _, existing := range s.volumes {
			if existing.Name == resource.Name {
				found = true
			}
		}
		if !found {
			labels := map[string]string{api.ProjectLabel: project.Name}
			for key, value := range resource.Labels {
				labels[key] = value
			}
			s.volumes = append(s.volumes, &volume.Volume{Name: resource.Name, Labels: labels})
		}
	}
	if s.afterUp != nil {
		s.afterUp()
	}
	return nil
}
func (s *scopedComposeTestService) Down(_ context.Context, name string, _ api.DownOptions) error {
	s.removed = append(s.removed, name)
	delete(s.containers, name)
	return nil
}

func (s *scopedComposeTestService) NetworkList(context.Context, network.ListOptions) ([]network.Summary, error) {
	return s.networks, s.resourceError
}
func (s *scopedComposeTestService) VolumeList(context.Context, volume.ListOptions) (volume.ListResponse, error) {
	return volume.ListResponse{Volumes: s.volumes}, s.resourceError
}

func scopedTestDeployer(t *testing.T, scope string, service *scopedComposeTestService) *ComposeDeployer {
	t.Helper()
	c, err := NewScopedComposeDeployer(scope)
	require.NoError(t, err)
	c.createComposeServiceFn = func(command.Cli, ...compose.Option) api.Compose { return service }
	c.scopeResourceClientFn = func(command.Cli) scopeResourceClient { return service }
	return c
}

func TestScopedNoContainersCannotReuseOrDeleteForeignResources(t *testing.T) {
	projectName := libstack.EdgeStackProjectName("blue", "vision")
	file := createFile(t, t.TempDir(), "compose.yml", "services:\n  detector:\n    image: unused:test\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n")
	for _, kind := range []string{"network", "volume"} {
		for _, owner := range []string{"", "green", "blue-wrong-project"} {
			t.Run(kind+"-"+owner, func(t *testing.T) {
				labels := map[string]string{api.ProjectLabel: projectName}
				if owner != "" {
					labels[libstack.EdgeStackScopeLabel] = owner
				}
				if owner == "blue-wrong-project" {
					labels[libstack.EdgeStackScopeLabel] = "blue"
					labels[api.ProjectLabel] = "another-project"
				}
				service := &scopedComposeTestService{containers: make(map[string][]api.ContainerSummary)}
				if kind == "network" {
					service.networks = []network.Summary{{Name: projectName + "_default", Labels: labels}}
				}
				if kind == "volume" {
					service.volumes = []*volume.Volume{{Name: projectName + "_data", Labels: labels}}
				}
				c := scopedTestDeployer(t, "blue", service)
				require.ErrorContains(t, c.Deploy(t.Context(), []string{file}, libstack.DeployOptions{Options: libstack.Options{ProjectName: projectName}, EdgeStackID: 1}), "foreign or legacy")
				require.Empty(t, service.upProjects, "foreign resource must be rejected before Up")
				// Down removes resources bearing its project label even if there
				// are no containers left; it must reject wrong-scope leftovers.
				if owner != "blue-wrong-project" {
					require.ErrorContains(t, c.Remove(t.Context(), projectName, nil, libstack.RemoveOptions{Volumes: true}), "foreign or legacy")
					require.Empty(t, service.removed)
				}
			})
		}
	}
}

func TestScopedPostUpRefusesResourceOwnershipRaceAndUnconfirmedCreation(t *testing.T) {
	projectName := libstack.EdgeStackProjectName("blue", "vision")
	file := createFile(t, t.TempDir(), "compose.yml", "services:\n  detector:\n    image: unused:test\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n")
	for _, scenario := range []string{"network-race", "volume-race", "missing-network", "missing-volume", "duplicate-network"} {
		t.Run(scenario, func(t *testing.T) {
			service := &scopedComposeTestService{containers: make(map[string][]api.ContainerSummary)}
			service.afterUp = func() {
				switch scenario {
				case "network-race":
					service.networks[0].Labels[libstack.EdgeStackScopeLabel] = "green"
				case "volume-race":
					service.volumes[0].Labels[api.ProjectLabel] = "legacy-project"
				case "missing-network":
					service.networks = nil
				case "missing-volume":
					service.volumes = nil
				case "duplicate-network":
					service.networks = append(service.networks, network.Summary{Name: projectName + "_default", Labels: map[string]string{api.ProjectLabel: "foreign"}})
				}
			}
			c := scopedTestDeployer(t, "blue", service)
			require.Error(t, c.Deploy(t.Context(), []string{file}, libstack.DeployOptions{Options: libstack.Options{ProjectName: projectName}, EdgeStackID: 1}), "SDK Up must not count as success without actual resource ownership")
			require.Len(t, service.upProjects, 1)
		})
	}
	service := &scopedComposeTestService{containers: make(map[string][]api.ContainerSummary), resourceError: errors.New("unavailable")}
	c := scopedTestDeployer(t, "blue", service)
	require.ErrorContains(t, c.Deploy(t.Context(), []string{file}, libstack.DeployOptions{Options: libstack.Options{ProjectName: projectName}, EdgeStackID: 1}), "unable to verify")
	require.ErrorContains(t, c.Remove(t.Context(), projectName, nil, libstack.RemoveOptions{}), "unable to verify")
	require.Empty(t, service.upProjects)
	require.Empty(t, service.removed)
}

func TestScopedProjectProofRejectsWrongProjectAndLegacyDualLabel(t *testing.T) {
	projectName := libstack.EdgeStackProjectName("blue", "vision")
	for _, scenario := range []string{"project", "legacy"} {
		t.Run(scenario, func(t *testing.T) {
			labels := map[string]string{api.ProjectLabel: projectName, libstack.EdgeStackScopeLabel: "blue", libstack.EdgeStackScopedIDLabel: "1"}
			if scenario == "project" {
				labels[api.ProjectLabel] = "another"
			} else {
				labels[PortainerEdgeStackLabel] = "1"
			}
			service := &scopedComposeTestService{containers: map[string][]api.ContainerSummary{projectName: {{Labels: labels}}}}
			c := scopedTestDeployer(t, "blue", service)
			require.ErrorContains(t, c.Remove(t.Context(), projectName, nil, libstack.RemoveOptions{}), "foreign or legacy")
			require.Empty(t, service.removed)
		})
	}
}

func TestScopedDiscoveryFiltersBeforeIDCollision(t *testing.T) {
	service := &scopedComposeTestService{containers: map[string][]api.ContainerSummary{
		"edge_voice":        {{Labels: map[string]string{PortainerEdgeStackLabel: "1", api.ProjectLabel: "edge_voice"}}},
		"edge_blue_vision":  {{Labels: map[string]string{libstack.EdgeStackScopedIDLabel: "1", libstack.EdgeStackScopeLabel: "blue", api.ProjectLabel: "edge_blue_vision"}}},
		"edge_green_vision": {{Labels: map[string]string{libstack.EdgeStackScopedIDLabel: "1", libstack.EdgeStackScopeLabel: "green", api.ProjectLabel: "edge_green_vision"}}},
		"edge_blue_dual":    {{Labels: map[string]string{PortainerEdgeStackLabel: "2", libstack.EdgeStackScopedIDLabel: "2", libstack.EdgeStackScopeLabel: "blue", api.ProjectLabel: "edge_blue_dual"}}},
		"edge_legacy_dual":  {{Labels: map[string]string{PortainerEdgeStackLabel: "3", libstack.EdgeStackScopedIDLabel: "3", api.ProjectLabel: "edge_legacy_dual"}}},
	}}
	for _, scope := range []string{"", "blue", "green"} {
		c := scopedTestDeployer(t, scope, service)
		stacks, err := c.GetExistingEdgeStacks(t.Context())
		require.NoError(t, err)
		require.Len(t, stacks, 1)
		require.Equal(t, scope, stacks[0].OwnerScope)
		expectedName := libstack.EdgeStackProjectName(scope, "vision")
		if scope == "" {
			expectedName = "edge_voice"
		}
		require.Equal(t, expectedName, stacks[0].Name)
	}
}

func TestScopedOwnDeployRestartAndRemovePreserveForeignProjects(t *testing.T) {
	foreign := api.ContainerSummary{Labels: map[string]string{PortainerEdgeStackLabel: "1", api.ProjectLabel: "edge_vision"}}
	service := &scopedComposeTestService{containers: map[string][]api.ContainerSummary{"edge_vision": {foreign}}}
	c := scopedTestDeployer(t, "blue", service)
	projectName := libstack.EdgeStackProjectName("blue", "vision")
	file := createFile(t, t.TempDir(), "compose.yml", "services:\n  detector:\n    image: unused:test\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n")
	require.NoError(t, c.Deploy(t.Context(), []string{file}, libstack.DeployOptions{Options: libstack.Options{ProjectName: projectName}, EdgeStackID: 1}))
	require.Len(t, service.upProjects, 1)
	labels := service.containers[projectName][0].Labels
	require.Equal(t, "blue", labels[libstack.EdgeStackScopeLabel])
	require.Equal(t, "1", labels[libstack.EdgeStackScopedIDLabel])
	require.NotContains(t, labels, PortainerEdgeStackLabel, "older Agents must not adopt scoped stacks")
	require.Equal(t, "blue", service.upProjects[0].Networks["default"].Labels[libstack.EdgeStackScopeLabel])
	require.Equal(t, "blue", service.upProjects[0].Volumes["data"].Labels[libstack.EdgeStackScopeLabel])
	green := scopedTestDeployer(t, "green", service)
	greenName := libstack.EdgeStackProjectName("green", "vision")
	require.NoError(t, green.Deploy(t.Context(), []string{file}, libstack.DeployOptions{Options: libstack.Options{ProjectName: greenName}, EdgeStackID: 1}))
	require.Len(t, service.containers, 3, "same stack ID and logical name in two scopes must coexist")
	require.Equal(t, "blue", service.containers[projectName][0].Labels[libstack.EdgeStackScopeLabel])
	require.Equal(t, "green", service.containers[greenName][0].Labels[libstack.EdgeStackScopeLabel])
	restarted := scopedTestDeployer(t, "blue", service)
	stacks, err := restarted.GetExistingEdgeStacks(t.Context())
	require.NoError(t, err)
	require.Equal(t, []libstack.EdgeStack{{ID: 1, Name: projectName, OwnerScope: "blue"}}, stacks)
	require.NoError(t, restarted.Remove(t.Context(), stacks[0].Name, nil, libstack.RemoveOptions{}))
	require.Equal(t, []api.ContainerSummary{foreign}, service.containers["edge_vision"])
	require.NotEmpty(t, service.containers[greenName], "removing one scope must preserve the other scope with the same ID/name")
	require.Equal(t, []string{projectName}, service.removed)
	// The same numeric ID and logical name in another scope has a distinct project.
	require.NotEqual(t, projectName, libstack.EdgeStackProjectName("green", "vision"))
}

func TestScopedMutationsRejectForeignProjectsAndGlobalContainerNames(t *testing.T) {
	projectName := libstack.EdgeStackProjectName("blue", "vision")
	for _, owner := range []string{"", "green"} {
		t.Run("existing-"+owner, func(t *testing.T) {
			service := &scopedComposeTestService{containers: map[string][]api.ContainerSummary{projectName: {{Labels: map[string]string{libstack.EdgeStackScopeLabel: owner}}}}}
			c := scopedTestDeployer(t, "blue", service)
			require.ErrorContains(t, c.Remove(t.Context(), projectName, nil, libstack.RemoveOptions{}), "foreign or legacy")
			file := createFile(t, t.TempDir(), "compose.yml", "services:\n  detector:\n    image: unused:test\n")
			require.ErrorContains(t, c.Deploy(t.Context(), []string{file}, libstack.DeployOptions{Options: libstack.Options{ProjectName: projectName}, EdgeStackID: 1}), "foreign or legacy")
			require.Empty(t, service.removed)
			require.Empty(t, service.upProjects)
		})
	}
	service := &scopedComposeTestService{containers: make(map[string][]api.ContainerSummary)}
	c := scopedTestDeployer(t, "blue", service)
	require.ErrorContains(t, c.Remove(t.Context(), "edge_vision", nil, libstack.RemoveOptions{}), "outside execution scope")
	file := createFile(t, t.TempDir(), "compose.yml", "services:\n  detector:\n    image: unused:test\n    container_name: protected-voice\n")
	require.ErrorContains(t, c.Deploy(t.Context(), []string{file}, libstack.DeployOptions{Options: libstack.Options{ProjectName: projectName}}), "container_name")
	require.Empty(t, service.upProjects)
	require.Empty(t, service.removed)
	for _, resource := range []string{"networks:\n  default:\n    name: protected-network\n", "volumes:\n  data:\n    name: protected-volume\n"} {
		content := "services:\n  detector:\n    image: unused:test\n"
		if resource[0] == 'v' {
			content += "    volumes:\n      - data:/data\n"
		}
		file := createFile(t, t.TempDir(), "compose.yml", content+resource)
		require.ErrorContains(t, c.Deploy(t.Context(), []string{file}, libstack.DeployOptions{Options: libstack.Options{ProjectName: projectName}}), "outside edge stack project scope")
	}
}
