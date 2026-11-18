package endpointedge

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/portainer/portainer/pkg/libstack"
	"github.com/stretchr/testify/require"
)

func TestExecutionScopeHeaderRequiresExactSingleScope(t *testing.T) {
	for _, test := range []struct {
		expected, actual string
		allowed          bool
	}{
		{"", "", true}, {"", "acceptance", false}, {"acceptance", "", false},
		{"acceptance", "production", false}, {"acceptance", "acceptance", true},
		{"acceptance", "ACCEPTANCE", false}, {"bad scope", "bad scope", false},
	} {
		h := &Handler{ExecutionScope: test.expected}
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if test.actual != "" {
			r.Header.Set(libstack.EdgeStackExecutionScopeHeader, test.actual)
		}
		require.Equal(t, test.allowed, h.executionScopeMatches(r))
	}
	h := &Handler{ExecutionScope: "acceptance"}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Add(libstack.EdgeStackExecutionScopeHeader, "acceptance")
	r.Header.Add(libstack.EdgeStackExecutionScopeHeader, "production")
	require.False(t, h.executionScopeMatches(r))
}

func TestScopeMismatchRefusesPollingAndAllStackPayloadsBeforeCache(t *testing.T) {
	// No datastore/authenticator is installed: a mismatched request must fail
	// before it can read status cache, advertise capability or fetch a payload.
	h := &Handler{ExecutionScope: "acceptance"}
	for _, value := range []string{"", "production"} {
		r := httptest.NewRequest(http.MethodGet, "/api/endpoints/4/edge/status", nil)
		if value != "" {
			r.Header.Set(libstack.EdgeStackExecutionScopeHeader, value)
		}
		err := h.endpointEdgeStatusInspect(httptest.NewRecorder(), r)
		require.NotNil(t, err)
		require.Equal(t, http.StatusForbidden, err.StatusCode)
		err = h.endpointEdgeStackInspect(httptest.NewRecorder(), r)
		require.NotNil(t, err)
		require.Equal(t, http.StatusForbidden, err.StatusCode)
	}
}
