package edgestackbusiness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edge"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/stretchr/testify/require"
)

type executionRoundTripper func(*http.Request) (*http.Response, error)

func (f executionRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func executionResponse(body any) *http.Response {
	raw, _ := json.Marshal(body)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}
}

func executionAuthorityFixture(t *testing.T) (*deliveryExecutionAuthority, edgestack.Task, string) {
	t.Helper()
	d, store, _ := dispatcherFixture(t)
	_ = d
	path := filepath.Join(t.TempDir(), "business.db")
	s, err := edgestack.Open(path, edgestack.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	task := edgestack.Task{ID: "test-task", Name: "Validation", DeviceID: "device", Kind: "voice", TestTask: true, Target: edgestack.Target{EndpointID: 4, StackID: 1, ServiceID: "voice"}}
	require.NoError(t, s.Bootstrap(context.Background(), edgestack.BootstrapConfig{Devices: []edgestack.Device{{ID: "device", Name: "Board", EndpointID: 4}}, Tasks: []edgestack.Task{task}}))
	stack := &portainer.EdgeStack{ID: 1, Name: "validation", Version: 1}
	require.NoError(t, store.EdgeStack().Create(stack.ID, stack))
	token := filepath.Join(t.TempDir(), "lease-token")
	require.NoError(t, os.WriteFile(token, []byte(strings.Repeat("s", 32)), 0600))
	a := &deliveryExecutionAuthority{service: s, store: store, baseURL: "http://lease.invalid", tokenFile: token, devices: map[string]string{"device": "board"}, client: &http.Client{}, capabilities: map[int]executionCapability{}}
	return a, task, path
}

func TestExecutionLeaseRenewalDoesNotExpireImmutableGrant(t *testing.T) {
	a, _, _ := executionAuthorityFixture(t)
	refused := false
	a.client.Transport = executionRoundTripper(func(r *http.Request) (*http.Response, error) {
		if refused {
			return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader("revoked"))}, nil
		}
		return executionResponse(map[string]any{"valid": true, "lease_id": "lease", "group_id": "board", "expires_at": float64(time.Now().Add(120*time.Second).UnixMilli()) / 1000}), nil
	})
	// A grant is bounded independently of the board's renewable 120s lease.
	grant := edge.ExecutionGrant{ResourceGroup: "board", LeaseID: "lease", FencingToken: 1, ControlEpoch: a.service.DeliveryControlEpoch(), OperationID: "operation", ExpectedStackVersion: 1, Action: "deploy", ExpiresAt: time.Now().Add(time.Hour)}
	lease := edgestack.DeliveryExecutionLease{ResourceGroup: "board", LeaseID: "lease", FencingToken: 1, ControlEpoch: a.service.DeliveryControlEpoch(), ExpiresAt: time.Now().Add(120 * time.Second)}
	require.NoError(t, grant.Validate(1, "pull", time.Now().Add(10*time.Minute)))
	lease.ExpiresAt = time.Now().Add(120 * time.Second)
	require.NoError(t, a.verifyLease(context.Background(), lease))
	refused = true
	require.Error(t, a.verifyLease(context.Background(), lease))
}

func TestManagedDeviceDeniesUnknownNativeStack(t *testing.T) {
	a, task, _ := executionAuthorityFixture(t)
	grant := edge.ExecutionGrant{ResourceGroup: "board", LeaseID: "lease", FencingToken: 1, ControlEpoch: a.service.DeliveryControlEpoch(), OperationID: "operation", ExpectedStackVersion: 1, Action: "deploy", ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, a.service.DeliverySaveExecutionBinding(edgestack.DeliveryExecutionBinding{EndpointID: 4, StackID: 1, TaskID: task.ID, DeviceID: task.DeviceID, Grant: grant}))
	managed, payload, err := a.Payload(context.Background(), 4, 99)
	require.True(t, managed)
	require.Nil(t, payload)
	require.ErrorContains(t, err, "managed_device_stack_not_authorized")
	managed, payload, err = a.Payload(context.Background(), 5, 99)
	require.False(t, managed)
	require.Nil(t, payload)
	require.NoError(t, err)
}

type executionStopClient struct {
	fakeMountClient
	stops int
}

func (c *executionStopClient) ContainerStop(_ context.Context, _ string, _ container.StopOptions) error {
	c.stops++
	c.rows[0].State = "exited"
	return nil
}

func TestAdministratorRecoveryFencesOldEpochAndStopsPausedContainer(t *testing.T) {
	a, task, path := executionAuthorityFixture(t)
	oldEpoch := a.service.DeliveryControlEpoch()
	grant := edge.ExecutionGrant{ResourceGroup: "board", LeaseID: "lease-old", FencingToken: 1, ControlEpoch: oldEpoch, OperationID: "old-op", ExpectedStackVersion: 1, Action: "deploy", ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, a.service.DeliverySaveExecutionBinding(edgestack.DeliveryExecutionBinding{EndpointID: 4, StackID: 1, TaskID: task.ID, DeviceID: task.DeviceID, Grant: grant}))
	require.NoError(t, a.service.DeliverySaveExecutionLease(edgestack.DeliveryExecutionLease{AuthorityJobID: "release:edge-execution:old-op", ResourceGroup: "board", LeaseID: "lease-old", FencingToken: 1, ControlEpoch: oldEpoch, ExpiresAt: time.Now().Add(time.Minute), ActiveOperationID: "old-op"}))
	require.NoError(t, a.service.Close())
	s, err := edgestack.Open(path, edgestack.Options{})
	require.NoError(t, err)
	defer s.Close()
	a.service = s
	require.NotEqual(t, oldEpoch, s.DeliveryControlEpoch())
	client := &executionStopClient{fakeMountClient: fakeMountClient{rows: []container.Summary{{ID: "paused-container", State: "paused", Labels: map[string]string{edgeStackLabel: "1", composeServiceLabel: "voice"}}}}}
	a.mounts = &mountProvider{newClient: func(context.Context, edgestack.Task) (mountClient, error) { return client, nil }}
	takeovers, releases := 0, 0
	a.client.Transport = executionRoundTripper(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/takeover-release") {
			takeovers++
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "release:edge-execution:old-op", body["previous_job_id"])
			require.True(t, strings.HasPrefix(body["job_id"].(string), "release:edge-recovery:"))
			return executionResponse(realLeaseFixture("active", "lease-new", body["job_id"].(string), 2, float64(time.Now().Add(120*time.Second).UnixMilli())/1000)), nil
		}
		if strings.HasSuffix(r.URL.Path, "/release") {
			releases++
			require.Equal(t, 1, client.stops)
			lease, _, err := s.DeliveryExecutionLease("board")
			require.NoError(t, err)
			return executionResponse(realLeaseFixture("released", "lease-new", lease.AuthorityJobID, 2, nil)), nil
		}
		if r.Method == http.MethodGet {
			return executionResponse(realLeaseFixture("active", "lease-old", "release:edge-execution:old-op", 1, float64(time.Now().Add(120*time.Second).UnixMilli())/1000)), nil
		}
		return executionResponse(map[string]any{"valid": true, "lease_id": "lease-new", "group_id": "board", "expires_at": float64(time.Now().Add(120*time.Second).UnixMilli()) / 1000}), nil
	})
	plan, err := a.previewExecutionStop(context.Background(), "board", 1)
	require.NoError(t, err)
	require.True(t, plan.Recovery)
	require.NoError(t, a.confirmExecutionStop(context.Background(), &plan))
	require.Equal(t, 1, takeovers)
	require.Equal(t, 1, releases)
	require.True(t, client.options.All)
	require.Empty(t, client.options.Filters.Get("status"))
	lease, known, err := s.DeliveryExecutionLease("board")
	require.NoError(t, err)
	require.True(t, known)
	require.True(t, lease.Released)
	require.Equal(t, uint64(2), lease.FencingToken)
	require.Equal(t, "released", plan.Status)
}
