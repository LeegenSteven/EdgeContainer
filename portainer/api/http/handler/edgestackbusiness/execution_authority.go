package edgestackbusiness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/dataservices"
	"github.com/portainer/portainer/api/edge"
	"github.com/portainer/portainer/api/edgestack"
)

type leaseAuthorityView struct {
	Valid          bool     `json:"valid"`
	GroupID        string   `json:"group_id"`
	ResourceGroups []string `json:"resource_groups"`
	ReleaseInfo    struct {
		RemoteStopConfirmed bool `json:"remote_stop_confirmed"`
	} `json:"release_info"`
	OwnerAgent    string            `json:"owner_agent"`
	Purpose       string            `json:"purpose"`
	JobID         string            `json:"job_id"`
	LeaseID       string            `json:"lease_id"`
	Status        string            `json:"status"`
	ExpiresAt     time.Time         `json:"expires_at"`
	FencingTokens map[string]uint64 `json:"fencing_tokens"`
}

// The real shared authority returns Unix seconds, while older adapters used
// RFC3339. Waiting/cancelled leases can have no expiry; a zero value never
// satisfies the separate live-lease checks.
func (v *leaseAuthorityView) UnmarshalJSON(data []byte) error {
	type plain leaseAuthorityView
	*v = leaseAuthorityView{}
	var wire struct {
		*plain
		ExpiresAt json.RawMessage `json:"expires_at"`
	}
	wire.plain = (*plain)(v)
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	raw := bytes.TrimSpace(wire.ExpiresAt)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &v.ExpiresAt); err != nil {
			return errors.New("invalid_shared_lease_expiry")
		}
	} else {
		seconds, err := strconv.ParseFloat(string(raw), 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds >= 253402300800 {
			return errors.New("invalid_shared_lease_expiry")
		}
		whole, fraction := math.Modf(seconds)
		v.ExpiresAt = time.Unix(int64(whole), int64(math.Round(fraction*1e9))).UTC()
	}
	if v.ExpiresAt.Unix() < 0 || v.ExpiresAt.Unix() > 253402300799 || v.ExpiresAt.Equal(time.Unix(0, 0)) {
		return errors.New("invalid_shared_lease_expiry")
	}
	return nil
}

func executionAuthorityJob(operationID string) string { return "release:edge-execution:" + operationID }
func executionRecoveryJob(planID string) string       { return "release:edge-recovery:" + planID }
func executionLeaseIdentity(view leaseAuthorityView, group, job string) bool {
	return view.LeaseID != "" && view.OwnerAgent == "edgestack_server" && view.Purpose == "deploy_write" && view.JobID == job && len(view.ResourceGroups) == 1 && view.ResourceGroups[0] == group && len(view.FencingTokens) == 1 && view.FencingTokens[group] > 0
}
func executionLeaseMatches(view leaseAuthorityView, value edgestack.DeliveryExecutionLease) bool {
	return executionLeaseIdentity(view, value.ResourceGroup, value.AuthorityJobID) && view.LeaseID == value.LeaseID && view.FencingTokens[value.ResourceGroup] == value.FencingToken
}

type executionCapability struct {
	value string
	at    time.Time
}

type deliveryExecutionAuthority struct {
	mounts       *mountProvider
	adminAccess  func(http.Handler) http.Handler
	service      *edgestack.Service
	store        dataservices.DataStore
	baseURL      string
	tokenFile    string
	devices      map[string]string
	client       *http.Client
	mu           sync.Mutex
	capabilities map[int]executionCapability
}

func newDeliveryExecutionAuthority(service *edgestack.Service, store dataservices.DataStore) (*deliveryExecutionAuthority, error) {
	authority := &deliveryExecutionAuthority{service: service, store: store, devices: map[string]string{}, capabilities: map[int]executionCapability{}, tokenFile: os.Getenv("EDGESTACK_LEASE_TOKEN_FILE"), baseURL: strings.TrimRight(os.Getenv("EDGESTACK_LEASE_URL"), "/")}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	authority.client = &http.Client{Timeout: 4 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("lease_authority_redirect_refused") }}
	file := os.Getenv("EDGESTACK_DELIVERY_RESOURCES_FILE")
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var config struct {
			Devices map[string]string `json:"devices"`
		}
		if err = json.Unmarshal(raw, &config); err != nil {
			return nil, err
		}
		authority.devices = config.Devices
	}
	if authority.baseURL != "" {
		parsed, err := url.Parse(authority.baseURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("invalid_lease_authority_url")
		}
	}
	return authority, nil
}

func (a *deliveryExecutionAuthority) call(ctx context.Context, method, path, key string, body any) (leaseAuthorityView, error) {
	var result leaseAuthorityView
	if a.baseURL == "" || a.tokenFile == "" {
		return result, errors.New("shared_lease_authority_unconfigured")
	}
	token, err := os.ReadFile(a.tokenFile)
	if err != nil {
		return result, err
	}
	secret := strings.TrimSpace(string(token))
	if len(secret) < 16 || len(secret) > 4096 || strings.ContainsAny(secret, "\r\n") {
		return result, errors.New("invalid_shared_lease_credential")
	}
	var input io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return result, err
		}
		input = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, input)
	if err != nil {
		return result, err
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := a.client.Do(request)
	if err != nil {
		return result, errors.New("shared_lease_authority_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("shared_lease_authority_refused_%d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return result, errors.New("invalid_shared_lease_response")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return result, nil
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, errors.New("invalid_shared_lease_response")
	}
	return result, nil
}

func (a *deliveryExecutionAuthority) ObserveCapability(_ context.Context, endpoint int, value string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.capabilities[endpoint] = executionCapability{value: value, at: time.Now()}
	return nil
}
func (a *deliveryExecutionAuthority) Preview(_ context.Context, endpointID int) error {
	a.mu.Lock()
	capability := a.capabilities[endpointID]
	a.mu.Unlock()
	endpoint, err := a.store.Endpoint().Endpoint(portainer.EndpointID(endpointID))
	if err != nil || endpoint.Type != portainer.EdgeAgentOnDockerEnvironment || capability.value != edge.ExecutionGrantsCapability || time.Since(capability.at) > 2*time.Minute {
		return errors.New("execution_capable_edge_agent_required")
	}
	if a.baseURL == "" || a.tokenFile == "" || len(a.devices) == 0 {
		return errors.New("shared_lease_authority_unconfigured")
	}
	return nil
}

func (a *deliveryExecutionAuthority) verifyLease(ctx context.Context, value edgestack.DeliveryExecutionLease) error {
	if value.Released || value.ControlEpoch != a.service.DeliveryControlEpoch() || !time.Now().Before(value.ExpiresAt) {
		return errors.New("execution_lease_expired")
	}
	view, err := a.call(ctx, http.MethodPost, "/v1/device-leases/"+url.PathEscape(value.LeaseID)+"/verify", "", map[string]any{"resource_group": value.ResourceGroup, "fencing_token": value.FencingToken})
	if err != nil {
		return err
	}
	if !view.Valid || view.LeaseID != value.LeaseID || view.GroupID != value.ResourceGroup || !time.Now().Before(view.ExpiresAt) {
		return errors.New("execution_lease_verification_unconfirmed")
	}
	return nil
}

func (a *deliveryExecutionAuthority) Prepare(ctx context.Context, operationID string, task edgestack.Task, version int, action string) error {
	if operationID == "" || version < 1 {
		return errors.New("execution_operation_required")
	}
	if err := a.Preview(ctx, task.Target.EndpointID); err != nil {
		return err
	}
	group := a.devices[task.DeviceID]
	if group == "" {
		return errors.New("device_resource_group_unconfigured")
	}
	// Serialize grant changes through the persisted group record. The lease stays
	// resident while a managed container serves, including after model validation.
	a.mu.Lock()
	defer a.mu.Unlock()
	lease, known, err := a.service.DeliveryExecutionLease(group)
	if err != nil {
		return err
	}
	if known && !lease.Released {
		if err = a.verifyLease(ctx, lease); err != nil {
			return err
		}
		if lease.ActiveOperationID != operationID {
			busy, e := a.service.DeliveryExecutionOperationBusy(lease.ActiveOperationID)
			if e != nil {
				return e
			}
			if busy {
				return errors.New("waiting_for_device_execution_operation")
			}
		}
	} else {
		view, e := a.call(ctx, http.MethodPost, "/v1/device-leases", "edge-execution:"+operationID, map[string]any{"resource_groups": []string{group}, "owner_agent": "edgestack_server", "purpose": "deploy_write", "job_id": executionAuthorityJob(operationID), "ttl_seconds": 120})
		if e != nil {
			return e
		}
		if view.Status == "waiting" && view.LeaseID != "" && view.OwnerAgent == "edgestack_server" && view.JobID == executionAuthorityJob(operationID) && view.Purpose == "deploy_write" && len(view.ResourceGroups) == 1 && view.ResourceGroups[0] == group {
			// Nothing was granted to an executor. Cancel the waiting request so it
			// cannot later acquire an orphan occupancy and expire unconfirmed.
			cancelled, cancelErr := a.call(ctx, http.MethodPost, "/v1/device-leases/"+url.PathEscape(view.LeaseID)+"/release", "", map[string]any{"remote_stop_confirmed": true, "evidence": "waiting acquisition cancelled before any execution grant or board command was issued"})
			identity := cancelled.LeaseID == view.LeaseID && cancelled.OwnerAgent == view.OwnerAgent && cancelled.Purpose == view.Purpose && cancelled.JobID == view.JobID && len(cancelled.ResourceGroups) == 1 && cancelled.ResourceGroups[0] == group
			waitingCancelled := cancelled.Status == "cancelled" && len(cancelled.FencingTokens) == 0
			// A waiting request can become active just before cancellation. No
			// executor received a grant, so a verified released reply is safe too.
			racedRelease := cancelled.Status == "released" && executionLeaseIdentity(cancelled, group, executionAuthorityJob(operationID))
			if cancelErr != nil || !identity || (!waitingCancelled && !racedRelease) || !cancelled.ReleaseInfo.RemoteStopConfirmed {
				return errors.New("waiting_execution_lease_cancellation_unconfirmed")
			}
		}
		if view.Status != "active" || !executionLeaseIdentity(view, group, executionAuthorityJob(operationID)) || !time.Now().Before(view.ExpiresAt) {
			return errors.New("waiting_for_device_execution_lease")
		}
		lease = edgestack.DeliveryExecutionLease{AuthorityJobID: executionAuthorityJob(operationID), ResourceGroup: group, LeaseID: view.LeaseID, FencingToken: view.FencingTokens[group], ControlEpoch: a.service.DeliveryControlEpoch(), ExpiresAt: view.ExpiresAt}
	}
	binding, exists, err := a.service.DeliveryExecutionBinding(task.Target.EndpointID, task.Target.StackID)
	if err != nil {
		return err
	}
	if exists && binding.Grant.OperationID == operationID && binding.Grant.ExpectedStackVersion == version {
		return a.verifyLease(ctx, lease)
	}
	lease.ActiveOperationID = operationID
	if err = a.service.DeliverySaveExecutionLease(lease); err != nil {
		return err
	}
	grant := edge.ExecutionGrant{ResourceGroup: group, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken, ControlEpoch: a.service.DeliveryControlEpoch(), OperationID: operationID, ExpectedStackVersion: version, ExpiresAt: time.Now().Add(time.Hour), Action: action}
	return a.service.DeliverySaveExecutionBinding(edgestack.DeliveryExecutionBinding{EndpointID: task.Target.EndpointID, StackID: task.Target.StackID, DeviceID: task.DeviceID, TaskID: task.ID, Grant: grant})
}

func (a *deliveryExecutionAuthority) Payload(ctx context.Context, endpoint, stack int) (bool, *edge.ExecutionGrant, error) {
	binding, known, err := a.service.DeliveryExecutionBinding(endpoint, stack)
	if err != nil {
		return true, nil, err
	}
	if !known {
		rows, e := a.service.DeliveryExecutionBindings()
		if e != nil {
			return true, nil, e
		}
		for _, row := range rows {
			if row.EndpointID == endpoint {
				return true, nil, errors.New("managed_device_stack_not_authorized")
			}
		}
		if a.service.DeliveryDeviceManaged(endpoint) {
			return true, nil, errors.New("managed_device_stack_not_authorized")
		}
		managed, e := a.service.DeliveryManagedStack(stack)
		if e != nil || managed {
			return true, nil, errors.New("managed_stack_target_not_authorized")
		}
		return false, nil, nil
	}
	grant := binding.Grant
	if err = a.Verify(ctx, endpoint, stack, grant.Action, grant); err != nil {
		return true, nil, err
	}
	return true, &grant, nil
}

func (a *deliveryExecutionAuthority) Verify(ctx context.Context, endpointID, stackID int, action string, grant edge.ExecutionGrant) error {
	if err := grant.Validate(grant.ExpectedStackVersion, action, time.Now()); err != nil {
		return err
	}
	binding, known, err := a.service.DeliveryExecutionBinding(endpointID, stackID)
	if err != nil || !known || binding.Grant != grant || grant.ControlEpoch != a.service.DeliveryControlEpoch() {
		return errors.New("execution_grant_changed")
	}
	native, err := a.store.EdgeStack().EdgeStack(portainer.EdgeStackID(stackID))
	if err != nil || native.Version != grant.ExpectedStackVersion {
		return errors.New("execution_stack_version_changed")
	}
	relation, err := a.store.EndpointRelation().EndpointRelation(portainer.EndpointID(endpointID))
	if err != nil || !relation.EdgeStacks[native.ID] {
		return errors.New("execution_endpoint_relation_changed")
	}
	if err = a.service.DeliveryExecutionOperationAllowed(binding); err != nil {
		return err
	}
	lease, known, err := a.service.DeliveryExecutionLease(grant.ResourceGroup)
	if err != nil || !known || lease.LeaseID != grant.LeaseID || lease.FencingToken != grant.FencingToken || lease.ActiveOperationID != grant.OperationID {
		return errors.New("execution_lease_changed")
	}
	return a.verifyLease(ctx, lease)
}

func (a *deliveryExecutionAuthority) renewLease(ctx context.Context, lease edgestack.DeliveryExecutionLease) error {
	view, err := a.call(ctx, http.MethodPost, "/v1/device-leases/"+url.PathEscape(lease.LeaseID)+"/heartbeat", "", map[string]any{"fencing_tokens": map[string]uint64{lease.ResourceGroup: lease.FencingToken}})
	if err != nil {
		return err
	}
	if view.Status != "active" || !executionLeaseMatches(view, lease) || !time.Now().Before(view.ExpiresAt) {
		return errors.New("execution_lease_renewal_unconfirmed")
	}
	lease.ExpiresAt = view.ExpiresAt
	return a.service.DeliverySaveExecutionLease(lease)
}

func (a *deliveryExecutionAuthority) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			leases, err := a.service.DeliveryExecutionLeases()
			if err == nil {
				for _, lease := range leases {
					if lease.Released || lease.ControlEpoch != a.service.DeliveryControlEpoch() || !time.Now().Before(lease.ExpiresAt) {
						continue
					}
					_ = a.renewLease(ctx, lease)
				}
			}
			a.mu.Unlock()
		}
	}
}

var nativeExecutionStackPath = regexp.MustCompile(`^/api/edge_stacks/([0-9]+)(?:/|$)`)
var nativeExecutionEndpointPath = regexp.MustCompile(`^/api/endpoints/([0-9]+)/docker(?:/|$)`)

func (a *deliveryExecutionAuthority) NativeGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/edgestack/internal/execution-leases/stop-validation-task" {
			a.internalStopValidationTask(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/edgestack/admin/execution-leases") {
			if a.adminAccess == nil {
				http.Error(w, "administrator_required", http.StatusForbidden)
				return
			}
			a.adminAccess(http.HandlerFunc(a.adminExecutionStop)).ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if match := nativeExecutionStackPath.FindStringSubmatch(r.URL.Path); match != nil && !strings.HasSuffix(r.URL.Path, "/status") {
			id, _ := strconv.Atoi(match[1])
			managed, err := a.service.DeliveryManagedStack(id)
			if err != nil || managed {
				http.Error(w, "managed_stack_requires_deployment_plan", http.StatusConflict)
				return
			}
		}
		if match := nativeExecutionEndpointPath.FindStringSubmatch(r.URL.Path); match != nil {
			id, _ := strconv.Atoi(match[1])
			rows, err := a.service.DeliveryExecutionBindings()
			if err != nil {
				http.Error(w, "execution_registry_unavailable", http.StatusServiceUnavailable)
				return
			}
			for _, row := range rows {
				if row.EndpointID == id {
					http.Error(w, "managed_device_requires_deployment_plan", http.StatusConflict)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
