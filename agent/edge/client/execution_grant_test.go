package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/portainer/agent"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edge"
	"github.com/portainer/portainer/pkg/fips"
	"github.com/stretchr/testify/require"
)

func TestExecutionGrantUsesAuthenticatedSyncClient(t *testing.T) {
	fips.InitFIPS(false)
	grant := edge.ExecutionGrant{ResourceGroup: "board-a", LeaseID: "lease-a", FencingToken: 2, ControlEpoch: "epoch-a", OperationID: "operation-a", ExpectedStackVersion: 3, ExpiresAt: time.Now().Add(time.Minute), Action: "deploy"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/endpoints/4/edge/stacks/9/execution/verify", r.URL.Path)
		require.Equal(t, "edge-identity", r.Header.Get(agent.HTTPEdgeIdentifierHeaderName))
		require.Equal(t, "1", r.Header.Get(edge.ExecutionGrantsHeader))
		var body struct {
			Grant  edge.ExecutionGrant `json:"grant"`
			Action string              `json:"action"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, grant.OperationID, body.Grant.OperationID)
		require.Equal(t, "pull", body.Action)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := &PortainerEdgeClient{httpClient: BuildHTTPClient(5, &agent.Options{}), serverAddress: server.URL, edgeID: "edge-identity", getEndpointIDFn: func() portainer.EndpointID { return 4 }}
	require.NoError(t, client.VerifyExecutionGrant(context.Background(), 9, 3, "pull", grant))
	require.Error(t, (&PortainerAsyncClient{}).VerifyExecutionGrant(context.Background(), 9, 3, "pull", grant))
}
