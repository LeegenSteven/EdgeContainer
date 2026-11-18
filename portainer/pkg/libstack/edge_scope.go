package libstack

import (
	"errors"
	"regexp"
)

const (
	EdgeStackScopeLabel           = "io.portainer.edge_stack_scope"
	EdgeStackScopedIDLabel        = "io.portainer.edge_stack_scoped_id"
	EdgeStackExecutionScopeHeader = "X-EdgeStack-Execution-Scope"
)

var edgeStackScopePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidateEdgeStackScope accepts an empty legacy scope or an explicit Docker
// Compose namespace. Underscores are reserved as the project-name delimiter.
func ValidateEdgeStackScope(scope string) error {
	if scope != "" && !edgeStackScopePattern.MatchString(scope) {
		return errors.New("edge stack scope must be 1-32 lowercase letters, digits or hyphens, starting with a letter or digit")
	}
	return nil
}

func EdgeStackProjectName(scope, logicalName string) string {
	if scope == "" {
		return "edge_" + logicalName
	}
	return "edge_" + scope + "_" + logicalName
}
