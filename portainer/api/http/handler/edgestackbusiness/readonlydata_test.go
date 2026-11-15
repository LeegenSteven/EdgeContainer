package edgestackbusiness

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/portainer/portainer/api/edgestack"
)

type fakeMountClient struct {
	rows                    []container.Summary
	actual                  container.InspectResponse
	listErr, inspectErr     error
	lists, inspects, closed atomic.Int32
	options                 container.ListOptions
	inspectedID             string
}

func (c *fakeMountClient) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	c.lists.Add(1)
	c.options = options
	return c.rows, c.listErr
}
func (c *fakeMountClient) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	c.inspects.Add(1)
	c.inspectedID = id
	return c.actual, c.inspectErr
}
func (c *fakeMountClient) Close() error { c.closed.Add(1); return nil }

func mountFixture() (*mountProvider, *fakeMountClient, edgestack.Task) {
	labels := map[string]string{edgeStackLabel: "4", composeServiceLabel: "voice"}
	client := &fakeMountClient{rows: []container.Summary{{ID: "registered-container", State: "running", Labels: labels}}, actual: container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "registered-container", State: &container.State{Running: true}}, Config: &container.Config{Labels: labels, Env: []string{"PRIVATE_ENV=never-return"}}, Mounts: []container.MountPoint{{Type: mount.TypeBind, Source: "/private/host-models", Destination: "/models", RW: false}, {Type: mount.TypeVolume, Name: "registered-events", Source: "/var/lib/docker/private-volume-path", Destination: "/events", RW: true}, {Type: mount.TypeBind, Source: "/private/other-secret", Destination: "/run/secrets/unrelated", RW: false}}}}
	provider := &mountProvider{now: time.Now, cache: make(map[mountCacheKey]*mountCacheEntry), newClient: func(context.Context, edgestack.Task) (mountClient, error) { return client, nil }}
	task := edgestack.Task{ID: "voice", Revision: 1, Target: edgestack.Target{EndpointID: 4, StackID: 4, ServiceID: "voice"}}
	return provider, client, task
}
func TestMountProviderUsesExactRegisteredLabelsAndReturnsOnlyVerification(t *testing.T) {
	provider, client, task := mountFixture()
	readOnly := true
	writable := false
	checks := provider.Verify(context.Background(), task, []edgestack.MountRequest{{ContainerPath: "/models", ExpectedReadOnly: &readOnly}, {ContainerPath: "/events", ExpectedReadOnly: &writable, VolumeName: "registered-events"}})
	if len(checks) != 2 || !checks[0].Verified || !checks[1].Verified || checks[0].ObservedAt.IsZero() {
		t.Fatal(checks)
	}
	labels := client.options.Filters.Get("label")
	if !hasString(labels, edgeStackLabel+"=4") || !hasString(labels, composeServiceLabel+"=voice") || !hasString(client.options.Filters.Get("status"), "running") || client.options.All {
		t.Fatalf("query widened: %+v", client.options)
	}
	if client.inspectedID != "registered-container" || client.inspects.Load() != 1 || client.closed.Load() != 1 {
		t.Fatal("incorrect inspect lifecycle")
	}
	encoded, _ := json.Marshal(checks)
	for _, secret := range []string{"host-models", "PRIVATE_ENV", "private-volume-path", "unrelated", "registered-container"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("raw Docker metadata leaked: %s", encoded)
		}
	}
	provider.Verify(context.Background(), task, []edgestack.MountRequest{{ContainerPath: "/models"}})
	if client.lists.Load() != 1 || client.inspects.Load() != 1 {
		t.Fatal("eight-second cache missed")
	}
	task.Revision++
	provider.Verify(context.Background(), task, []edgestack.MountRequest{{ContainerPath: "/models"}})
	if client.lists.Load() != 2 {
		t.Fatal("revision change reused stale target observation")
	}
}
func hasString(values []string, wanted string) bool {
	for _, v := range values {
		if v == wanted {
			return true
		}
	}
	return false
}

func TestMountProviderFailsConservatively(t *testing.T) {
	for _, scenario := range []struct {
		name, reason string
		mutate       func(*fakeMountClient)
		request      edgestack.MountRequest
	}{
		{"multiple", "multiple_running_containers", func(c *fakeMountClient) { second := c.rows[0]; second.ID = "second"; c.rows = append(c.rows, second) }, edgestack.MountRequest{ContainerPath: "/models"}},
		{"stopped-list", "no_running_container", func(c *fakeMountClient) { c.rows[0].State = "exited" }, edgestack.MountRequest{ContainerPath: "/models"}},
		{"stopped-inspect", "container_not_running", func(c *fakeMountClient) { c.actual.State.Running = false }, edgestack.MountRequest{ContainerPath: "/models"}},
		{"wrong-list-label", "no_running_container", func(c *fakeMountClient) {
			c.rows[0].Labels = map[string]string{edgeStackLabel: "other", composeServiceLabel: "voice"}
		}, edgestack.MountRequest{ContainerPath: "/models"}},
		{"wrong-inspect-label", "container_identity_mismatch", func(c *fakeMountClient) {
			c.actual.Config.Labels = map[string]string{edgeStackLabel: "4", composeServiceLabel: "other"}
		}, edgestack.MountRequest{ContainerPath: "/models"}},
		{"wrong-inspect-id", "container_identity_mismatch", func(c *fakeMountClient) { c.actual.ID = "replaced" }, edgestack.MountRequest{ContainerPath: "/models"}},
		{"missing-path", "mount_not_found", func(*fakeMountClient) {}, edgestack.MountRequest{ContainerPath: "/models/file"}},
		{"read-only", "mount_read_only_mismatch", func(*fakeMountClient) {}, edgestack.MountRequest{ContainerPath: "/models", ExpectedReadOnly: boolPointer(false)}},
		{"volume-name", "mount_volume_mismatch", func(*fakeMountClient) {}, edgestack.MountRequest{ContainerPath: "/events", VolumeName: "other-volume"}},
		{"bind-not-volume", "mount_volume_mismatch", func(*fakeMountClient) {}, edgestack.MountRequest{ContainerPath: "/models", VolumeName: "registered-events"}},
		{"invalid-path", "invalid_container_path", func(*fakeMountClient) {}, edgestack.MountRequest{ContainerPath: "/models/../secret"}},
		{"list-error", "mount_query_unavailable", func(c *fakeMountClient) { c.listErr = errors.New("private detail must not escape") }, edgestack.MountRequest{ContainerPath: "/models"}},
		{"inspect-error", "mount_query_unavailable", func(c *fakeMountClient) { c.inspectErr = errors.New("private detail must not escape") }, edgestack.MountRequest{ContainerPath: "/models"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			provider, client, task := mountFixture()
			scenario.mutate(client)
			checks := provider.Verify(context.Background(), task, []edgestack.MountRequest{scenario.request})
			if len(checks) != 1 || checks[0].Verified || checks[0].Reason != scenario.reason || !checks[0].ObservedAt.IsZero() {
				t.Fatal(checks)
			}
		})
	}
}
func boolPointer(value bool) *bool { return &value }

func TestMountProviderSharesInFlightQueryAndHonorsCallerDeadline(t *testing.T) {
	provider, client, task := mountFixture()
	opened := make(chan struct{})
	release := make(chan struct{})
	var factories atomic.Int32
	provider.newClient = func(context.Context, edgestack.Task) (mountClient, error) {
		if factories.Add(1) == 1 {
			close(opened)
		}
		<-release
		return client, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	checks := provider.Verify(ctx, task, []edgestack.MountRequest{{ContainerPath: "/models"}})
	if time.Since(start) > 300*time.Millisecond || checks[0].Reason != "mount_query_timeout" || checks[0].Verified {
		t.Fatal(checks)
	}
	<-opened
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	provider.Verify(ctx2, task, []edgestack.MountRequest{{ContainerPath: "/models"}})
	if factories.Load() != 1 {
		t.Fatal("blocked tunnel opened duplicate queries")
	}
	close(release)
	checks = provider.Verify(context.Background(), task, []edgestack.MountRequest{{ContainerPath: "/models"}})
	if !checks[0].Verified || client.lists.Load() != 1 {
		t.Fatal(checks)
	}
}

func TestMountObservationExpiresAfterEightSeconds(t *testing.T) {
	provider, client, task := mountFixture()
	clock := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	provider.now = func() time.Time { return clock }
	first := provider.Verify(context.Background(), task, []edgestack.MountRequest{{ContainerPath: "/models"}})
	clock = clock.Add(7 * time.Second)
	cached := provider.Verify(context.Background(), task, []edgestack.MountRequest{{ContainerPath: "/models"}})
	if client.lists.Load() != 1 || !cached[0].ObservedAt.Equal(first[0].ObservedAt) {
		t.Fatal("observation did not remain cached for eight seconds")
	}
	clock = clock.Add(2 * time.Second)
	fresh := provider.Verify(context.Background(), task, []edgestack.MountRequest{{ContainerPath: "/models"}})
	if client.lists.Load() != 2 || !fresh[0].ObservedAt.After(first[0].ObservedAt) {
		t.Fatal("expired observation was reused")
	}
}

func TestRuntimeObservationRequiresUniqueRunningInspectAndSharesMountCache(t *testing.T) {
	for _, state := range []string{"running", "multiple", "stopped", "error"} {
		t.Run(state, func(t *testing.T) {
			provider, client, task := mountFixture()
			switch state {
			case "multiple":
				second := client.rows[0]
				second.ID = "other"
				client.rows = append(client.rows, second)
			case "stopped":
				client.actual.State.Running = false
			case "error":
				client.inspectErr = errors.New("unavailable")
			}
			observation := provider.ObserveRuntime(context.Background(), task)
			if state == "running" {
				if observation.State != "running" || observation.ObservedAt.IsZero() {
					t.Fatal(observation)
				}
				provider.Verify(context.Background(), task, []edgestack.MountRequest{{ContainerPath: "/models"}})
				if client.lists.Load() != 1 || client.inspects.Load() != 1 {
					t.Fatal("runtime and mount facts did not share a snapshot")
				}
			} else if observation.State != "unknown" || !observation.ObservedAt.IsZero() {
				t.Fatalf("failure fabricated running process: %+v", observation)
			}
		})
	}
}
