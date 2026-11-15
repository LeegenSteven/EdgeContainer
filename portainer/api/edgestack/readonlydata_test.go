package edgestack

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

type mountProviderFunc func(context.Context, Task, []MountRequest) []MountVerification

func (f mountProviderFunc) Verify(ctx context.Context, task Task, requests []MountRequest) []MountVerification {
	return f(ctx, task, requests)
}

func registeredMountFixture(t *testing.T, f *fixture) {
	t.Helper()
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		task, e := read[Task](tx, "tasks", "task-a")
		if e != nil {
			return e
		}
		task.DataLocations = []json.RawMessage{json.RawMessage(`{"name":"Models","location":"Worker model directory","containerPath":"/models","expectedReadOnly":true}`), json.RawMessage(`{"name":"Server records","kind":"server","containerPath":"/data"}`), json.RawMessage(`{"name":"Legacy location","path":"/registered-only","verification":"verified","observedAt":"2026-11-15T12:00:00Z"}`)}
		return put(tx, "tasks", task.ID, task)
	}); err != nil {
		t.Fatal(err)
	}
}
func TestMountProjectionIsAuthorizedNarrowAndOutsideBusinessTransaction(t *testing.T) {
	f := setup(t)
	registeredMountFixture(t, f)
	var calls atomic.Int32
	var wrote atomic.Bool
	f.s.opts.MountProvider = mountProviderFunc(func(ctx context.Context, task Task, requests []MountRequest) []MountVerification {
		calls.Add(1)
		if task.ID != "task-a" || task.Target.StackID != 1 || len(requests) != 1 || requests[0].ContainerPath != "/models" || requests[0].ExpectedReadOnly == nil || !*requests[0].ExpectedReadOnly {
			t.Errorf("query escaped registration: %+v %+v", task, requests)
		}
		// A synchronous write would deadlock if projection still held a
		// business.db read transaction while doing the provider call.
		if err := f.s.db.Update(func(tx *bolt.Tx) error {
			source, e := read[Source](tx, "sources", "source-a")
			if e != nil {
				return e
			}
			source.ErrorCode = "provider_write_probe"
			return put(tx, "sources", source.ID, source)
		}); err != nil {
			t.Error(err)
		}
		wrote.Store(true)
		return []MountVerification{{Verified: true, ObservedAt: f.now}}
	})
	start := time.Now()
	device := f.ok(t, "alice", "GET", "/devices/device-a", nil, 200)
	if !wrote.Load() || time.Since(start) > time.Second {
		t.Fatal("mount query held a business transaction")
	}
	locations := device["tasks"].([]any)[0].(map[string]any)["dataLocations"].([]any)
	verified := locations[0].(map[string]any)
	if verified["verification"] != "verified" || verified["observedAt"] == nil || verified["location"] != "Worker model directory" {
		t.Fatal(verified)
	}
	server := locations[1].(map[string]any)
	if server["verification"] != "registered" || server["verificationReason"] != "server_managed_records" {
		t.Fatal(server)
	}
	legacy := locations[2].(map[string]any)
	if legacy["verification"] != "registered" || legacy["observedAt"] != nil {
		t.Fatal("operator asserted verification was trusted")
	}
	f.ok(t, "bob", "GET", "/devices/device-a", nil, 404)
	f.ok(t, "alice", "GET", "/devices/device-b", nil, 404)
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		task, e := read[Task](tx, "tasks", "task-a")
		if e != nil {
			return e
		}
		task.UserIDs = nil
		task.TeamIDs = nil
		return put(tx, "tasks", task.ID, task)
	}); err != nil {
		t.Fatal(err)
	}
	f.ok(t, "alice", "GET", "/devices/device-a", nil, 200)
	if calls.Load() != 1 {
		t.Fatal("unauthorized task triggered Docker query")
	}
	f.endpointAllowed = false
	f.ok(t, "alice", "GET", "/devices/device-a", nil, 404)
	if calls.Load() != 1 {
		t.Fatal("denied endpoint triggered Docker query")
	}
}
func TestMountProjectionRechecksPermissionAndTargetAfterQuery(t *testing.T) {
	for _, change := range []string{"permission", "target"} {
		t.Run(change, func(t *testing.T) {
			f := setup(t)
			registeredMountFixture(t, f)
			f.s.opts.MountProvider = mountProviderFunc(func(context.Context, Task, []MountRequest) []MountVerification {
				if change == "permission" {
					f.endpointAllowed = false
				} else if err := f.s.db.Update(func(tx *bolt.Tx) error {
					task, e := read[Task](tx, "tasks", "task-a")
					if e != nil {
						return e
					}
					task.Revision++
					return put(tx, "tasks", task.ID, task)
				}); err != nil {
					t.Error(err)
				}
				return []MountVerification{{Verified: true, ObservedAt: f.now}}
			})
			expected := 404
			if change == "target" {
				expected = 409
			}
			response := f.request(t, "alice", "GET", "/devices/device-a", nil)
			if response.Code != expected || strings.Contains(response.Body.String(), "observedAt") {
				t.Fatalf("stale/unauthorized result escaped: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
func TestMountProjectionTimeoutStaysRegistered(t *testing.T) {
	f := setup(t)
	registeredMountFixture(t, f)
	f.s.opts.MountProvider = mountProviderFunc(func(ctx context.Context, _ Task, _ []MountRequest) []MountVerification { <-ctx.Done(); return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", "/api/edgestack/devices/device-a", nil).WithContext(ctx)
	r.Header.Set("X-Test-User", "alice")
	response := httptest.NewRecorder()
	start := time.Now()
	f.handler.ServeHTTP(response, r)
	if response.Code != 200 || time.Since(start) > 500*time.Millisecond || !strings.Contains(response.Body.String(), `"verification":"registered"`) || !strings.Contains(response.Body.String(), "mount_query_timeout") {
		t.Fatalf("timeout not conservative: %d %s", response.Code, response.Body.String())
	}
}
