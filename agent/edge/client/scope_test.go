package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/portainer/agent"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edge"
	"github.com/portainer/portainer/pkg/fips"
	"github.com/portainer/portainer/pkg/libstack"
	"github.com/stretchr/testify/require"
)

func TestExecutionScopeAdvertisedOnPollConfigAndGrantVerification(t *testing.T) {
	fips.InitFIPS(false)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "blue", r.Header.Get(libstack.EdgeStackExecutionScopeHeader))
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := NewPortainerClient(server.URL, nil, func() portainer.EndpointID { return 4 }, "edge", "", false, agent.PlatformDocker, agent.EdgeMetaFields{}, BuildHTTPClient(5, &agent.Options{}), false, WithExecutionScope("blue"))
	_, err := client.GetEnvironmentStatus()
	require.NoError(t, err)
	version := 3
	_, err = client.GetEdgeStackConfig(9, &version)
	require.NoError(t, err)
	grant := edge.ExecutionGrant{ResourceGroup: "board", LeaseID: "lease", FencingToken: 1, ControlEpoch: "epoch", OperationID: "op", ExpectedStackVersion: 3, ExpiresAt: time.Now().Add(time.Minute), Action: "deploy"}
	require.NoError(t, client.(ExecutionGrantVerifier).VerifyExecutionGrant(context.Background(), 9, 3, "pull", grant))
	require.Equal(t, 3, requests)
}
