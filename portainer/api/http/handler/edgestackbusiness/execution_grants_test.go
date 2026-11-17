package edgestackbusiness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/datastore"
	"github.com/portainer/portainer/api/edge"
	"github.com/stretchr/testify/require"
)

func TestExecutionGrantHandlerAuthenticatesAndFailsClosed(t *testing.T) {
	_, store := datastore.MustNewTestStore(t, true, false)
	endpoint := portainer.Endpoint{ID: 4, Name: "edge", Type: portainer.EdgeAgentOnDockerEnvironment, EdgeID: "edge-identity"}
	require.NoError(t, store.Endpoint().Create(&endpoint))
	for _, scenario := range []string{"ok", "identity", "capability", "expired", "missing_authority", "authority_refused", "trailing_json"} {
		t.Run(scenario, func(t *testing.T) {
			grant := edge.ExecutionGrant{ResourceGroup: "board-a", LeaseID: "lease-a", FencingToken: 2, ControlEpoch: "epoch-a", OperationID: "operation-a", ExpectedStackVersion: 3, ExpiresAt: time.Now().Add(time.Minute), Action: "deploy"}
			if scenario == "expired" {
				grant.ExpiresAt = time.Now().Add(-time.Second)
			}
			payload, _ := json.Marshal(map[string]any{"grant": grant, "action": "pull"})
			if scenario == "trailing_json" {
				payload = append(payload, []byte(" {}")...)
			}
			called := false
			check := ExecutionGrantCheck(func(_ context.Context, endpointID, stackID int, action string, got edge.ExecutionGrant) error {
				called = true
				require.Equal(t, 4, endpointID)
				require.Equal(t, 9, stackID)
				require.Equal(t, "pull", action)
				require.Equal(t, grant.OperationID, got.OperationID)
				if scenario == "authority_refused" {
					return errors.New("lease_revoked")
				}
				return nil
			})
			if scenario == "missing_authority" {
				check = nil
			}
			handler := NewExecutionGrantHandler(store, check)
			request := httptest.NewRequest(http.MethodPost, "/api/endpoints/4/edge/stacks/9/execution/verify", bytes.NewReader(payload))
			request.Header.Set(portainer.PortainerAgentEdgeIDHeader, "edge-identity")
			request.Header.Set(edge.ExecutionGrantsHeader, edge.ExecutionGrantsCapability)
			if scenario == "identity" {
				request.Header.Set(portainer.PortainerAgentEdgeIDHeader, "foreign-identity")
			}
			if scenario == "capability" {
				request.Header.Del(edge.ExecutionGrantsHeader)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			want := http.StatusNoContent
			switch scenario {
			case "identity", "capability":
				want = http.StatusForbidden
			case "expired", "authority_refused":
				want = http.StatusConflict
			case "missing_authority":
				want = http.StatusServiceUnavailable
			case "trailing_json":
				want = http.StatusBadRequest
			}
			require.Equal(t, want, response.Code)
			require.Equal(t, scenario == "ok" || scenario == "authority_refused", called)
		})
	}
}
