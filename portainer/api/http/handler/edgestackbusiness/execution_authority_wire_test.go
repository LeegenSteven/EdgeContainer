package edgestackbusiness

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/portainer/portainer/api/edge"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/stretchr/testify/require"
)

// Flat fixture mirrors the actual Python LeaseManager._view response, including
// numeric timestamps and nullable timestamps for waiting/released records.
func realLeaseFixture(status, id, job string, fence uint64, expiry any) map[string]any {
	return map[string]any{"lease_id": id, "owner_agent": "edgestack_server", "job_id": job, "purpose": "deploy_write", "status": status, "ttl_seconds": 120.0, "resource_groups": []string{"board"}, "fencing_tokens": map[string]uint64{"board": fence}, "expires_at": expiry, "release_info": map[string]any{"remote_stop_confirmed": status == "released" || status == "cancelled"}}
}

func TestSharedLeaseTimestampWireFormats(t *testing.T) {
	for _, test := range []struct {
		name, wire  string
		valid, zero bool
	}{
		{"unix_integer", `{"expires_at":1794923502}`, true, false},
		{"unix_fraction", `{"expires_at":1794923502.093}`, true, false},
		{"rfc3339", `{"expires_at":"2026-11-17T13:51:42.093Z"}`, true, false},
		{"released_null", `{"status":"released","expires_at":null}`, true, true},
		{"missing", `{"status":"waiting"}`, true, true},
		{"empty", `{"expires_at":""}`, false, false},
		{"string_number", `{"expires_at":"1794923502"}`, false, false},
		{"zero", `{"expires_at":0}`, false, false},
		{"negative", `{"expires_at":-1}`, false, false},
		{"nonfinite", `{"expires_at":1e309}`, false, false},
		{"nan", `{"expires_at":NaN}`, false, false},
		{"out_of_range", `{"expires_at":253402300800}`, false, false},
		{"before_epoch", `{"expires_at":"0001-01-01T00:00:00Z"}`, false, false},
		{"offset_out_of_range", `{"expires_at":"9999-12-31T23:59:59-23:59"}`, false, false},
		{"object", `{"expires_at":{}}`, false, false},
		{"boolean", `{"expires_at":true}`, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var view leaseAuthorityView
			err := json.Unmarshal([]byte(test.wire), &view)
			if !test.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.zero, view.ExpiresAt.IsZero())
			if test.name == "unix_fraction" {
				require.InDelta(t, 1794923502.093, float64(view.ExpiresAt.UnixNano())/1e9, 0.000001)
			}
		})
	}
}

func TestRealLeaseAcquireAndHeartbeatNumericFixtures(t *testing.T) {
	a, task, _ := executionAuthorityFixture(t)
	require.NoError(t, a.ObserveCapability(context.Background(), 4, edge.ExecutionGrantsCapability, ""))
	operation := "wire-operation"
	expiry := float64(time.Now().Add(120*time.Second).UnixMilli()) / 1000
	a.client.Transport = executionRoundTripper(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		if r.URL.Path == "/v1/device-leases" {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "release:edge-execution:"+operation, body["job_id"])
			require.Equal(t, "deploy_write", body["purpose"])
			require.Equal(t, "edgestack_server", body["owner_agent"])
			require.Equal(t, float64(120), body["ttl_seconds"])
			require.Equal(t, "edge-execution:"+operation, r.Header.Get("Idempotency-Key"))
		} else {
			require.Equal(t, "/v1/device-leases/lease-wire/heartbeat", r.URL.Path)
			require.Equal(t, float64(4), body["fencing_tokens"].(map[string]any)["board"])
		}
		return executionResponse(realLeaseFixture("active", "lease-wire", executionAuthorityJob(operation), 4, expiry)), nil
	})
	require.NoError(t, a.Prepare(context.Background(), operation, task, 2, "deploy"))
	lease, known, err := a.service.DeliveryExecutionLease("board")
	require.NoError(t, err)
	require.True(t, known)
	require.Equal(t, executionAuthorityJob(operation), lease.AuthorityJobID)
	binding, known, err := a.service.DeliveryExecutionBinding(4, 1)
	require.NoError(t, err)
	require.True(t, known)
	require.Equal(t, operation, binding.Grant.OperationID)
	expiry += 120
	require.NoError(t, a.renewLease(context.Background(), lease))
	updated, _, err := a.service.DeliveryExecutionLease("board")
	require.NoError(t, err)
	require.True(t, updated.ExpiresAt.After(lease.ExpiresAt))
}

func TestRealLeaseAcquireRejectsForeignOrIncompleteIdentity(t *testing.T) {
	for _, field := range []string{"owner_agent", "purpose", "job_id", "resource_groups", "fencing_tokens", "expires_at"} {
		t.Run(field, func(t *testing.T) {
			a, task, _ := executionAuthorityFixture(t)
			require.NoError(t, a.ObserveCapability(context.Background(), 4, edge.ExecutionGrantsCapability, ""))
			a.client.Transport = executionRoundTripper(func(r *http.Request) (*http.Response, error) {
				view := realLeaseFixture("active", "lease-wire", executionAuthorityJob("op"), 4, float64(time.Now().Add(time.Minute).Unix()))
				switch field {
				case "resource_groups":
					view[field] = []string{"other"}
				case "fencing_tokens":
					view[field] = map[string]int{"board": 4, "other": 5}
				case "expires_at":
					view[field] = nil
				default:
					view[field] = "other"
				}
				return executionResponse(view), nil
			})
			require.Error(t, a.Prepare(context.Background(), "op", task, 2, "deploy"))
			_, known, err := a.service.DeliveryExecutionLease("board")
			require.NoError(t, err)
			require.False(t, known)
		})
	}
}

func TestRealLeaseVerifyRequiresActualValidIdentityAndLiveExpiry(t *testing.T) {
	for _, field := range []string{"ok", "valid", "lease_id", "group_id", "expires_at"} {
		t.Run(field, func(t *testing.T) {
			a, _, _ := executionAuthorityFixture(t)
			lease := edgestack.DeliveryExecutionLease{AuthorityJobID: executionAuthorityJob("op"), ResourceGroup: "board", LeaseID: "lease-wire", FencingToken: 4, ControlEpoch: a.service.DeliveryControlEpoch(), ExpiresAt: time.Now().Add(time.Minute)}
			a.client.Transport = executionRoundTripper(func(r *http.Request) (*http.Response, error) {
				var request map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				require.Equal(t, float64(4), request["fencing_token"])
				require.Equal(t, "board", request["resource_group"])
				view := map[string]any{"valid": true, "lease_id": "lease-wire", "group_id": "board", "expires_at": float64(time.Now().Add(time.Minute).UnixMilli()) / 1000}
				switch field {
				case "valid":
					view[field] = false
				case "lease_id", "group_id":
					view[field] = "other"
				case "expires_at":
					view[field] = nil
				}
				return executionResponse(view), nil
			})
			err := a.verifyLease(context.Background(), lease)
			if field == "ok" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestWaitingLeaseIsCancelledBeforeAnyGrantIsPublished(t *testing.T) {
	a, task, _ := executionAuthorityFixture(t)
	require.NoError(t, a.ObserveCapability(context.Background(), 4, edge.ExecutionGrantsCapability, ""))
	cancelled := false
	a.client.Transport = executionRoundTripper(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/release") {
			cancelled = true
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, true, body["remote_stop_confirmed"])
			view := realLeaseFixture("cancelled", "lease-wait", executionAuthorityJob("op"), 0, nil)
			view["fencing_tokens"] = map[string]uint64{}
			return executionResponse(view), nil
		}
		view := realLeaseFixture("waiting", "lease-wait", executionAuthorityJob("op"), 0, nil)
		view["fencing_tokens"] = map[string]uint64{}
		return executionResponse(view), nil
	})
	require.ErrorContains(t, a.Prepare(context.Background(), "op", task, 2, "deploy"), "waiting_for_device_execution_lease")
	require.True(t, cancelled)
	_, known, err := a.service.DeliveryExecutionBinding(4, 1)
	require.NoError(t, err)
	require.False(t, known)
}

func TestRealLeaseRejectsMalformedExpiryFromHttpResponse(t *testing.T) {
	a, _, _ := executionAuthorityFixture(t)
	a.client.Transport = executionRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"lease_id":"lease","expires_at":1e309}`))}, nil
	})
	_, err := a.call(context.Background(), http.MethodGet, "/v1/device-leases/lease", "", nil)
	require.ErrorContains(t, err, "invalid_shared_lease_response")
}

func TestRealLeaseHeartbeatRejectsChangedOwnerFenceAndStatus(t *testing.T) {
	for _, field := range []string{"owner_agent", "purpose", "job_id", "lease_id", "fencing_tokens", "resource_groups", "status", "expires_at"} {
		t.Run(field, func(t *testing.T) {
			a, _, _ := executionAuthorityFixture(t)
			lease := edgestack.DeliveryExecutionLease{AuthorityJobID: executionAuthorityJob("op"), ResourceGroup: "board", LeaseID: "lease-wire", FencingToken: 4, ControlEpoch: a.service.DeliveryControlEpoch(), ExpiresAt: time.Now().Add(time.Minute)}
			require.NoError(t, a.service.DeliverySaveExecutionLease(lease))
			a.client.Transport = executionRoundTripper(func(*http.Request) (*http.Response, error) {
				view := realLeaseFixture("active", "lease-wire", lease.AuthorityJobID, 4, float64(time.Now().Add(240*time.Second).Unix()))
				switch field {
				case "fencing_tokens":
					view[field] = map[string]int{"board": 5}
				case "resource_groups":
					view[field] = []string{"other"}
				case "expires_at":
					view[field] = nil
				default:
					view[field] = "other"
				}
				return executionResponse(view), nil
			})
			require.Error(t, a.renewLease(context.Background(), lease))
			saved, _, err := a.service.DeliveryExecutionLease("board")
			require.NoError(t, err)
			require.True(t, saved.ExpiresAt.Equal(lease.ExpiresAt))
		})
	}
}

func TestRealLeaseReleasedReplayRequiresOwnerFenceAndStopProof(t *testing.T) {
	for _, field := range []string{"ok", "owner_agent", "purpose", "job_id", "fencing_tokens", "release_info"} {
		t.Run(field, func(t *testing.T) {
			a, task, _ := executionAuthorityFixture(t)
			a.mounts = &mountProvider{}
			lease := edgestack.DeliveryExecutionLease{AuthorityJobID: executionRecoveryJob("stop-plan"), ResourceGroup: "board", LeaseID: "lease-wire", FencingToken: 4, ControlEpoch: a.service.DeliveryControlEpoch(), ExpiresAt: time.Now().Add(time.Minute), ActiveOperationID: "stop-plan"}
			require.NoError(t, a.service.DeliverySaveExecutionLease(lease))
			a.client.Transport = executionRoundTripper(func(*http.Request) (*http.Response, error) {
				view := realLeaseFixture("released", "lease-wire", lease.AuthorityJobID, 4, nil)
				switch field {
				case "owner_agent", "purpose", "job_id":
					view[field] = "other"
				case "fencing_tokens":
					view[field] = map[string]int{"board": 5}
				case "release_info":
					view[field] = nil
				}
				return executionResponse(view), nil
			})
			plan := edgestack.DeliveryExecutionStopPlan{ID: "stop-plan", ResourceGroup: "board", LeaseID: "lease-wire", Stopped: true, Targets: []edgestack.DeliveryExecutionStopTarget{{TaskID: task.ID, StackID: 1, Version: 1}}}
			err := a.confirmExecutionStop(context.Background(), &plan)
			if field == "ok" {
				require.NoError(t, err)
				require.Equal(t, "released", plan.Status)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestRealLeaseTakeoverRejectsForeignSuccessorBeforeStop(t *testing.T) {
	for _, field := range []string{"owner_agent", "purpose", "job_id", "lease_id", "fencing_tokens", "resource_groups", "status"} {
		t.Run(field, func(t *testing.T) {
			a, task, _ := executionAuthorityFixture(t)
			a.mounts = &mountProvider{}
			lease := edgestack.DeliveryExecutionLease{AuthorityJobID: executionAuthorityJob("op"), ResourceGroup: "board", LeaseID: "lease-old", FencingToken: 4, ControlEpoch: a.service.DeliveryControlEpoch(), ExpiresAt: time.Now().Add(time.Minute), ActiveOperationID: "op"}
			require.NoError(t, a.service.DeliverySaveExecutionLease(lease))
			a.client.Transport = executionRoundTripper(func(r *http.Request) (*http.Response, error) {
				require.True(t, strings.HasSuffix(r.URL.Path, "/takeover-release"))
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.True(t, strings.HasPrefix(body["job_id"].(string), "release:"))
				require.Equal(t, lease.AuthorityJobID, body["previous_job_id"])
				view := realLeaseFixture("active", "lease-new", executionRecoveryJob("stop-plan"), 5, float64(time.Now().Add(time.Minute).Unix()))
				switch field {
				case "lease_id":
					view[field] = "lease-old"
				case "fencing_tokens":
					view[field] = map[string]int{"board": 7}
				case "resource_groups":
					view[field] = []string{"other"}
				default:
					view[field] = "other"
				}
				return executionResponse(view), nil
			})
			plan := edgestack.DeliveryExecutionStopPlan{ID: "stop-plan", ResourceGroup: "board", LeaseID: "lease-old", Recovery: true, PreviousJobID: lease.AuthorityJobID, Targets: []edgestack.DeliveryExecutionStopTarget{{TaskID: task.ID, StackID: 1, Version: 1}}}
			require.Error(t, a.confirmExecutionStop(context.Background(), &plan))
			saved, _, err := a.service.DeliveryExecutionLease("board")
			require.NoError(t, err)
			require.Equal(t, "lease-old", saved.LeaseID)
		})
	}
}
