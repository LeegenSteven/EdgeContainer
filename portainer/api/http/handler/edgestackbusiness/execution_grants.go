package edgestackbusiness

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/dataservices"
	"github.com/portainer/portainer/api/edge"
	"github.com/portainer/portainer/pkg/libstack"
)

// ExecutionGrantCheck must compare the whole grant with the persisted managed
// operation, current control-plane epoch, endpoint and stack assignment, and
// synchronously verify the shared lease authority. A nil check denies access.
type ExecutionGrantCheck func(context.Context, int, int, string, edge.ExecutionGrant) error

// NewExecutionGrantHandler authenticates the existing Edge identifier and then
// delegates lease validation to the model-deployment control plane. Mount this
// exact path before any broader endpoint-edge route.
func NewExecutionGrantHandler(store dataservices.DataStore, verify ExecutionGrantCheck, scopes ...string) http.Handler {
	expectedScope := ""
	if len(scopes) == 1 {
		expectedScope = scopes[0]
	}
	router := mux.NewRouter()
	router.HandleFunc("/api/endpoints/{id}/edge/stacks/{stackId}/execution/verify", func(w http.ResponseWriter, r *http.Request) {
		scope := r.Header.Get(libstack.EdgeStackExecutionScopeHeader)
		if len(scopes) > 1 || len(r.Header.Values(libstack.EdgeStackExecutionScopeHeader)) > 1 || libstack.ValidateEdgeStackScope(expectedScope) != nil || libstack.ValidateEdgeStackScope(scope) != nil || scope != expectedScope {
			http.Error(w, "execution_scope_mismatch", http.StatusForbidden)
			return
		}
		endpointID, e1 := strconv.Atoi(mux.Vars(r)["id"])
		stackID, e2 := strconv.Atoi(mux.Vars(r)["stackId"])
		if e1 != nil || e2 != nil || endpointID < 1 || stackID < 1 {
			http.Error(w, "invalid_execution_target", http.StatusBadRequest)
			return
		}
		endpoint, err := store.Endpoint().Endpoint(portainer.EndpointID(endpointID))
		identifier := r.Header.Get(portainer.PortainerAgentEdgeIDHeader)
		if err != nil || endpoint.EdgeID == "" || identifier == "" || subtle.ConstantTimeCompare([]byte(endpoint.EdgeID), []byte(identifier)) != 1 || endpoint.Type != portainer.EdgeAgentOnDockerEnvironment || r.Header.Get(edge.ExecutionGrantsHeader) != edge.ExecutionGrantsCapability {
			http.Error(w, "execution_identity_invalid", http.StatusForbidden)
			return
		}
		var payload struct {
			Grant  edge.ExecutionGrant `json:"grant"`
			Action string              `json:"action"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			http.Error(w, "invalid_execution_request", http.StatusBadRequest)
			return
		}
		if decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "invalid_execution_request", http.StatusBadRequest)
			return
		}
		if err := payload.Grant.Validate(payload.Grant.ExpectedStackVersion, payload.Action, time.Now()); err != nil {
			http.Error(w, "execution_grant_invalid", http.StatusConflict)
			return
		}
		if verify == nil {
			http.Error(w, "execution_authority_unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := verify(r.Context(), endpointID, stackID, payload.Action, payload.Grant); err != nil {
			http.Error(w, "execution_grant_refused", http.StatusConflict)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}).Methods(http.MethodPost)
	return router
}
