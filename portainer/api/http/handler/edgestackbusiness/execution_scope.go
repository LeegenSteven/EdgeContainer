package edgestackbusiness

import (
	"errors"
	"strconv"

	"github.com/docker/docker/api/types/filters"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/pkg/libstack"
)

const composeProjectLabel = "com.docker.compose.project"

// A numeric stack ID is local to one Server database. An explicit execution
// scope makes container identity disjoint from legacy and other Server scopes.
type executionContainerTarget struct {
	scope, stackID, service, project string
}

func containerTarget(scope string, target edgestack.Target) (executionContainerTarget, error) {
	if err := libstack.ValidateEdgeStackScope(scope); err != nil {
		return executionContainerTarget{}, err
	}
	if target.StackID < 1 || target.ServiceID == "" || (scope != "" && target.StackName == "") {
		return executionContainerTarget{}, errors.New("container_scope_target_unavailable")
	}
	identity := executionContainerTarget{scope: scope, stackID: strconv.Itoa(target.StackID), service: target.ServiceID}
	if scope != "" {
		identity.project = libstack.EdgeStackProjectName(scope, target.StackName)
	}
	return identity, nil
}

func (t executionContainerTarget) filters() filters.Args {
	args := filters.NewArgs(filters.Arg("label", composeServiceLabel+"="+t.service))
	if t.scope == "" {
		args.Add("label", edgeStackLabel+"="+t.stackID)
	} else {
		args.Add("label", libstack.EdgeStackScopedIDLabel+"="+t.stackID)
		args.Add("label", libstack.EdgeStackScopeLabel+"="+t.scope)
		args.Add("label", composeProjectLabel+"="+t.project)
	}
	return args
}

func (t executionContainerTarget) matches(labels map[string]string) bool {
	if labels[composeServiceLabel] != t.service {
		return false
	}
	if t.scope == "" {
		return labels[edgeStackLabel] == t.stackID && labels[libstack.EdgeStackScopeLabel] == "" && labels[libstack.EdgeStackScopedIDLabel] == ""
	}
	return labels[libstack.EdgeStackScopedIDLabel] == t.stackID && labels[libstack.EdgeStackScopeLabel] == t.scope && labels[composeProjectLabel] == t.project && labels[edgeStackLabel] == ""
}

func (p *mountProvider) containerTarget(task edgestack.Task) (executionContainerTarget, error) {
	target := task.Target
	if p.executionScope != "" && p.stackName != nil {
		name, err := p.stackName(target.StackID)
		if err != nil {
			return executionContainerTarget{}, errors.New("container_scope_target_unavailable")
		}
		target.StackName = name
	}
	return containerTarget(p.executionScope, target)
}
