package edgestacks

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	portainer "github.com/portainer/portainer/api"

	"github.com/segmentio/encoding/json"
	"github.com/stretchr/testify/require"
)

// Update Status
func TestUpdateStatusAndInspect(t *testing.T) {
	t.Parallel()
	handler, rawAPIKey := setupHandler(t)

	endpoint := createEndpoint(t, handler.DataStore)
	edgeStack := createEdgeStack(t, handler.DataStore, endpoint.ID)

	// Update edge stack status
	newStatus := portainer.EdgeStackStatusError
	payload := updateStatusPayload{
		Error:      "test-error",
		Status:     &newStatus,
		EndpointID: endpoint.ID,
	}

	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)

	r := bytes.NewBuffer(jsonPayload)
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("/edge_stacks/%d/status", edgeStack.ID), r)
	require.NoError(t, err)

	req.Header.Set(portainer.PortainerAgentEdgeIDHeader, endpoint.EdgeID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected a %d response, found: %d", http.StatusOK, rec.Code)
	}

	// Get updated edge stack
	req, err = http.NewRequest(http.MethodGet, fmt.Sprintf("/edge_stacks/%d", edgeStack.ID), nil)
	require.NoError(t, err)

	req.Header.Add("x-api-key", rawAPIKey)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected a %d response, found: %d", http.StatusOK, rec.Code)
	}

	updatedStack := portainer.EdgeStack{}
	err = json.NewDecoder(rec.Body).Decode(&updatedStack)
	require.NoError(t, err)

	endpointStatus, ok := updatedStack.Status[payload.EndpointID]
	require.True(t, ok)

	lastStatus := endpointStatus.Status[len(endpointStatus.Status)-1]

	if len(endpointStatus.Status) == len(edgeStack.Status[payload.EndpointID].Status) {
		t.Fatal("expected status array to be updated")
	}

	if lastStatus.Type != *payload.Status {
		t.Fatalf("expected EdgeStackStatusType %d, found %d", *payload.Status, lastStatus.Type)
	}

	if endpointStatus.EndpointID != payload.EndpointID {
		t.Fatalf("expected EndpointID %d, found %d", payload.EndpointID, endpointStatus.EndpointID)
	}
}

func TestCompletedStatusIsBoundToReportedStackVersion(t *testing.T) {
	handler, _ := setupHandler(t)
	endpoint := createEndpoint(t, handler.DataStore)
	stack := createEdgeStack(t, handler.DataStore, endpoint.ID)
	stack.Version = 3
	require.NoError(t, handler.DataStore.EdgeStack().UpdateEdgeStack(stack.ID, &stack))
	complete := portainer.EdgeStackStatusCompleted
	submit := func(version int) {
		data, err := json.Marshal(updateStatusPayload{Status: &complete, EndpointID: endpoint.ID, Version: version})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/edge_stacks/%d/status", stack.ID), bytes.NewReader(data))
		req.Header.Set(portainer.PortainerAgentEdgeIDHeader, endpoint.EdgeID)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}
	for _, version := range []int{0, 2, 4, 3, 0, 3} {
		submit(version)
		status, err := handler.DataStore.EdgeStackStatus().Read(stack.ID, endpoint.ID)
		require.NoError(t, err)
		var completed []int
		for _, transition := range status.Status {
			if transition.Type == complete {
				completed = append(completed, transition.Version)
			}
		}
		if version == 2 || version == 4 {
			require.Equal(t, []int{0}, completed)
		}
		if version == 3 {
			require.Equal(t, []int{3}, completed)
		}
	}
}

func TestUpdateStatusForUnassignedEdgeStack(t *testing.T) {
	t.Parallel()
	handler, _ := setupHandler(t)

	endpoint := createEndpoint(t, handler.DataStore)
	unrelatedEndpoint := createEndpointWithId(t, handler.DataStore, 6)
	edgeStack := createEdgeStack(t, handler.DataStore, endpoint.ID)

	newStatus := portainer.EdgeStackStatusRunning
	payload := updateStatusPayload{
		Status:     &newStatus,
		EndpointID: unrelatedEndpoint.ID,
	}

	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)

	r := bytes.NewBuffer(jsonPayload)
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("/edge_stacks/%d/status", edgeStack.ID), r)
	require.NoError(t, err)

	req.Header.Set(portainer.PortainerAgentEdgeIDHeader, unrelatedEndpoint.EdgeID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)

	_, err = handler.DataStore.EdgeStackStatus().Read(edgeStack.ID, unrelatedEndpoint.ID)
	require.True(t, handler.DataStore.IsErrObjectNotFound(err))
}

func TestUpdateStatusRemovedAllowedAfterUnassignment(t *testing.T) {
	t.Parallel()
	handler, _ := setupHandler(t)

	endpoint := createEndpoint(t, handler.DataStore)
	edgeStack := createEdgeStack(t, handler.DataStore, endpoint.ID)

	// Simulate the endpoint being unassigned from the stack (e.g. edge group changed)
	// before the agent has torn down and reported its final status back.
	relation, err := handler.DataStore.EndpointRelation().EndpointRelation(endpoint.ID)
	require.NoError(t, err)

	delete(relation.EdgeStacks, edgeStack.ID)
	require.NoError(t, handler.DataStore.EndpointRelation().UpdateEndpointRelation(endpoint.ID, relation))

	newStatus := portainer.EdgeStackStatusRemoved
	payload := updateStatusPayload{
		Status:     &newStatus,
		EndpointID: endpoint.ID,
	}

	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)

	r := bytes.NewBuffer(jsonPayload)
	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("/edge_stacks/%d/status", edgeStack.ID), r)
	require.NoError(t, err)

	req.Header.Set(portainer.PortainerAgentEdgeIDHeader, endpoint.EdgeID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestUpdateStatusWithInvalidPayload(t *testing.T) {
	t.Parallel()
	handler, _ := setupHandler(t)

	endpoint := createEndpoint(t, handler.DataStore)
	edgeStack := createEdgeStack(t, handler.DataStore, endpoint.ID)

	// Update edge stack status
	statusError := portainer.EdgeStackStatusError
	statusOk := portainer.EdgeStackStatusDeploymentReceived
	cases := []struct {
		Name                 string
		Payload              updateStatusPayload
		ExpectedErrorMessage string
		ExpectedStatusCode   int
	}{
		{
			"Update with nil Status",
			updateStatusPayload{
				Error:      "test-error",
				Status:     nil,
				EndpointID: endpoint.ID,
			},
			"Invalid status",
			400,
		},
		{
			"Update with error status and empty error message",
			updateStatusPayload{
				Error:      "",
				Status:     &statusError,
				EndpointID: endpoint.ID,
			},
			"Error message is mandatory when status is error",
			400,
		},
		{
			"Update with missing EndpointID",
			updateStatusPayload{
				Error:      "",
				Status:     &statusOk,
				EndpointID: 0,
			},
			"Invalid EnvironmentID",
			400,
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			jsonPayload, err := json.Marshal(tc.Payload)
			require.NoError(t, err)

			r := bytes.NewBuffer(jsonPayload)
			req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("/edge_stacks/%d/status", edgeStack.ID), r)
			require.NoError(t, err)

			req.Header.Set(portainer.PortainerAgentEdgeIDHeader, endpoint.EdgeID)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.ExpectedStatusCode {
				t.Fatalf("expected a %d response, found: %d", tc.ExpectedStatusCode, rec.Code)
			}
		})
	}
}
