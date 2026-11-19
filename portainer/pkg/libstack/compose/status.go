package compose

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/portainer/portainer/api/logs"
	"github.com/portainer/portainer/pkg/libstack"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/cli/cli/command"
	"github.com/docker/compose/v2/pkg/api"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

type publisher struct {
	URL           string
	TargetPort    int
	PublishedPort int
	Protocol      string
}

type service struct {
	ID         string
	Name       string
	Image      string
	Command    string
	Project    string
	Service    string
	Created    int64
	State      string
	Status     string
	Health     string
	ExitCode   int
	Publishers []publisher
}

// docker container state can be one of "created", "running", "paused", "restarting", "removing", "exited", or "dead"
func getServiceStatus(ctx context.Context, service service) (libstack.Status, string) {
	log.Debug().
		Str("service", service.Name).
		Str("state", service.State).
		Int("exitCode", service.ExitCode).
		Msg("getServiceStatus")

	switch service.State {
	case "created", "restarting", "paused":
		return libstack.StatusStarting, ""
	case "running":
		return libstack.StatusRunning, ""
	case "removing":
		return libstack.StatusRemoving, ""
	case "exited":
		if service.ExitCode == 0 {
			return libstack.StatusCompleted, ""
		}

		errorMessage, err := getContainerLogsTail(ctx, service)
		if err != nil {
			log.Error().
				Err(err).
				Str("service", service.Name).
				Msg("failed to get logs from container")
			errorMessage = fmt.Sprintf("service %s exited with code %d", service.Name, service.ExitCode)
		}

		return libstack.StatusError, errorMessage
	case "dead":
		if service.ExitCode == 0 {
			return libstack.StatusRemoved, ""
		}

		errorMessage, err := getContainerLogsTail(ctx, service)
		if err != nil {
			log.Error().
				Err(err).
				Str("service", service.Name).
				Msg("failed to get logs from container")
			errorMessage = fmt.Sprintf("service %s exited with code %d", service.Name, service.ExitCode)
		}

		return libstack.StatusError, errorMessage
	default:
		return libstack.StatusUnknown, ""
	}
}

func getContainerLogsTail(ctx context.Context, service service) (string, error) {
	var combinedOutput bytes.Buffer

	if err := libstack.WithCli(ctx, libstack.DockerCliOptions{}, func(ctx context.Context, cli *command.DockerCli) error {
		out, err := cli.Client().ContainerLogs(ctx, service.Name, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Timestamps: true,
			Follow:     false,
			Tail:       "20",
		})
		if err != nil {
			return errors.Wrap(err, "unable to get logs from container")
		}
		defer logs.CloseAndLogErr(out)

		_, err = io.Copy(&combinedOutput, out)
		if err != nil {
			return errors.Wrap(err, "unable to read container logs")
		}

		return nil
	}); err != nil {
		return "", errors.Wrap(err, "unable to get logs from container")
	}

	return combinedOutput.String(), nil
}

func aggregateStatuses(ctx context.Context, services []service) (libstack.Status, string) {
	servicesCount := len(services)

	if servicesCount == 0 {
		log.Debug().
			Msg("no services found")

		return libstack.StatusRemoved, ""
	}

	statusCounts := make(map[libstack.Status]int)
	errorMessage := ""
	for _, service := range services {
		status, serviceError := getServiceStatus(ctx, service)
		if serviceError != "" {
			errorMessage = serviceError
		}
		statusCounts[status]++
	}

	log.Debug().
		Interface("statusCounts", statusCounts).
		Str("errorMessage", errorMessage).
		Msg("check_status")

	if errorMessage != "" {
		return libstack.StatusError, errorMessage
	}

	return libstack.AggregateStatusCounts(statusCounts, servicesCount), ""
}

// WaitForDeployment observes the same interpolated service model used by Up.
// A completed zero-replica project remains registered for a later scale-up.
func (c *ComposeDeployer) WaitForDeployment(ctx context.Context, file string, options libstack.DeployOptions) libstack.WaitResult {
	if err := c.validateProjectScope(options.ProjectName); err != nil {
		return libstack.WaitResult{Status: libstack.StatusError, ErrorMsg: err.Error()}
	}
	project, err := createProject(ctx, []string{file}, options.Options)
	if err != nil || len(project.Services) == 0 {
		return libstack.WaitResult{Status: libstack.StatusError, ErrorMsg: "unable to determine desired compose services"}
	}
	for _, svc := range project.Services {
		if svc.GetScale() > 0 {
			return c.waitForServices(ctx, options.Options, libstack.StatusRunning, project, int(options.EdgeStackID))
		}
	}

	return waitForObservation(ctx, libstack.StatusCompleted, func(ctx context.Context) (libstack.Status, string, error) {
		var rows []container.Summary
		err := libstack.WithCli(ctx, libstack.DockerCliOptions{Host: options.Host}, func(_ context.Context, cli *command.DockerCli) error {
			var err error
			// Compose Ps only includes containers bearing its config-hash label.
			// Query the whole project so a foreign or orphaned container cannot disappear from the proof.
			rows, err = cli.Client().ContainerList(ctx, container.ListOptions{
				All: true, Filters: filters.NewArgs(filters.Arg("label", api.ProjectLabel+"="+project.Name)),
			})
			return err
		})
		if err != nil {
			return libstack.StatusUnknown, "", err
		}
		for _, row := range rows {
			if !c.ownsDeploymentContainer(project.Name, int(options.EdgeStackID), row.Labels) {
				return libstack.StatusError, "zero-replica project contains a foreign container", nil
			}
			if row.State != "exited" && row.State != "dead" {
				return libstack.StatusStarting, "", nil
			}
		}
		return libstack.StatusCompleted, "", nil
	})
}

func (c *ComposeDeployer) ownsDeploymentContainer(name string, stackID int, labels map[string]string) bool {
	if labels[api.ProjectLabel] != name {
		return false
	}
	legacyID, legacy := labels[PortainerEdgeStackLabel]
	scope, scoped := labels[libstack.EdgeStackScopeLabel]
	scopedID, scopedStack := labels[libstack.EdgeStackScopedIDLabel]
	id := strconv.Itoa(stackID)
	if c.ownerScope != "" {
		return !legacy && scope == c.ownerScope && scopedID == id
	}
	return legacyID == id && !scoped && !scopedStack
}

func (c *ComposeDeployer) WaitForStatus(ctx context.Context, name string, status libstack.Status) libstack.WaitResult {
	return c.waitForServices(ctx, libstack.Options{ProjectName: name}, status, nil, 0)
}

func (c *ComposeDeployer) waitForServices(ctx context.Context, options libstack.Options, status libstack.Status, project *types.Project, stackID int) libstack.WaitResult {
	return waitForObservation(ctx, status, func(ctx context.Context) (libstack.Status, string, error) {
		var rows []api.ContainerSummary
		name := options.ProjectName
		err := c.withComposeService(ctx, nil, options, func(service api.Compose, _ *types.Project) error {
			var err error
			rows, err = service.Ps(ctx, name, api.PsOptions{All: true})
			return err
		})
		if err != nil {
			return libstack.StatusUnknown, "", err
		}
		if project != nil {
			// Missing replicas and one-off containers cannot satisfy a new deployment.
			counts := make(map[string]int)
			current := make([]api.ContainerSummary, 0, len(rows))
			for _, row := range rows {
				svc, declared := project.Services[row.Service]
				if !declared || strings.EqualFold(row.Labels[api.OneoffLabel], "true") {
					continue
				}
				if stackID > 0 && !c.ownsDeploymentContainer(name, stackID, row.Labels) {
					return libstack.StatusError, "compose project contains a foreign container", nil
				}
				if svc.GetScale() == 0 && (row.State == "exited" || row.State == "dead") {
					continue
				}
				current = append(current, row)
				counts[row.Service]++
			}
			observed, msg := aggregateStatuses(ctx, serviceListFromContainerSummary(current))
			if observed == libstack.StatusRunning || observed == libstack.StatusCompleted {
				for name, svc := range project.Services {
					if counts[name] != svc.GetScale() {
						return libstack.StatusStarting, "", nil
					}
				}
			}
			return observed, msg, nil
		}
		observed, msg := aggregateStatuses(ctx, serviceListFromContainerSummary(rows))
		return observed, msg, nil
	})
}

func waitForObservation(ctx context.Context, desired libstack.Status, observe func(context.Context) (libstack.Status, string, error)) libstack.WaitResult {
	for ctx.Err() == nil {
		pollCtx, cancel := context.WithTimeout(ctx, time.Minute)
		observed, msg, err := observe(pollCtx)
		cancel()
		if ctx.Err() != nil {
			break
		}
		if err == nil {
			if msg != "" {
				return libstack.WaitResult{Status: libstack.StatusError, ErrorMsg: msg}
			}
			if observed == desired || (desired == libstack.StatusRunning && observed == libstack.StatusCompleted) {
				return libstack.WaitResult{Status: observed}
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	return libstack.WaitResult{Status: desired, ErrorMsg: "failed to wait for status: " + ctx.Err().Error()}
}

func serviceListFromContainerSummary(containerSummaries []api.ContainerSummary) []service {
	var services []service

	for _, cs := range containerSummaries {
		var publishers []publisher

		for _, p := range cs.Publishers {
			publishers = append(publishers, publisher{
				URL:           p.URL,
				TargetPort:    p.TargetPort,
				PublishedPort: p.PublishedPort,
				Protocol:      p.Protocol,
			})
		}

		services = append(services, service{
			ID:         cs.ID,
			Name:       cs.Name,
			Image:      cs.Image,
			Command:    cs.Command,
			Project:    cs.Project,
			Service:    cs.Service,
			Created:    cs.Created,
			State:      cs.State,
			Status:     cs.Status,
			Health:     cs.Health,
			ExitCode:   cs.ExitCode,
			Publishers: publishers,
		})
	}

	return services
}
