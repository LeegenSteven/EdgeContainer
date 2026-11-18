package endpointedge

import (
	"github.com/portainer/portainer/pkg/libstack"
	"net/http"
)

func (handler *Handler) executionScopeMatches(r *http.Request) bool {
	values := r.Header.Values(libstack.EdgeStackExecutionScopeHeader)
	if len(values) > 1 {
		return false
	}
	scope := r.Header.Get(libstack.EdgeStackExecutionScopeHeader)
	return libstack.ValidateEdgeStackScope(handler.ExecutionScope) == nil && libstack.ValidateEdgeStackScope(scope) == nil && scope == handler.ExecutionScope
}
