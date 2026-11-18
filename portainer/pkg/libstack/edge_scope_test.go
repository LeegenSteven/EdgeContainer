package libstack

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEdgeStackScopeValidationAndProjectNames(t *testing.T) {
	for _, scope := range []string{"", "blue", "model-delivery-20261117", strings.Repeat("a", 32)} {
		require.NoError(t, ValidateEdgeStackScope(scope))
	}
	for _, scope := range []string{"Blue", "a_b", "../blue", " blue", "-blue", strings.Repeat("a", 33)} {
		require.Error(t, ValidateEdgeStackScope(scope))
	}
	require.Equal(t, "edge_vision", EdgeStackProjectName("", "vision"))
	require.Equal(t, "edge_blue_vision", EdgeStackProjectName("blue", "vision"))
	require.NotEqual(t, EdgeStackProjectName("blue", "vision"), EdgeStackProjectName("green", "vision"))
}
