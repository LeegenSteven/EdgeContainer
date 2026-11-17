package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/portainer/agent"
	"github.com/portainer/portainer/api/edge"
)

// ExecutionGrantVerifier is optional so legacy/unmanaged clients keep their
// current behavior. A managed stack requires this interface.
type ExecutionGrantVerifier interface {
	VerifyExecutionGrant(context.Context, int, int, string, edge.ExecutionGrant) error
}

func (client *PortainerEdgeClient) VerifyExecutionGrant(ctx context.Context, stackID, version int, action string, grant edge.ExecutionGrant) error {
	if err := grant.Validate(version, action, time.Now()); err != nil {
		return err
	}
	endpointID := client.getEndpointIDFn()
	if endpointID < 1 || stackID < 1 {
		return errors.New("execution_target_invalid")
	}
	body, err := json.Marshal(struct {
		Grant  edge.ExecutionGrant `json:"grant"`
		Action string              `json:"action"`
	}{grant, action})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/endpoints/%d/edge/stacks/%d/execution/verify", client.serverAddress, endpointID, stackID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set(agent.HTTPEdgeIdentifierHeaderName, client.edgeID)
	request.Header.Set(edge.ExecutionGrantsHeader, edge.ExecutionGrantsCapability)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("execution authority unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("execution authority refused grant: HTTP %d", response.StatusCode)
	}
	return nil
}

// Async workers cannot contact the authority immediately before execution.
// They therefore never advertise this capability or execute managed stacks.
func (client *PortainerAsyncClient) VerifyExecutionGrant(context.Context, int, int, string, edge.ExecutionGrant) error {
	return errors.New("managed_execution_requires_synchronous_edge_agent")
}
