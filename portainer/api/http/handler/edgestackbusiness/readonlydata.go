package edgestackbusiness

import (
	"context"
	"errors"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/distribution/reference"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/dataservices"
	dockerclient "github.com/portainer/portainer/api/docker/client"
	"github.com/portainer/portainer/api/edgestack"
)

const edgeStackLabel = "io.portainer.edge_stack_id"
const composeServiceLabel = "com.docker.compose.service"
const mountQueryTimeout = 2 * time.Second
const mountCacheTTL = 8 * time.Second

type mountClient interface {
	ContainerList(context.Context, container.ListOptions) ([]container.Summary, error)
	ContainerInspect(context.Context, string) (container.InspectResponse, error)
	Close() error
}
type observedMount struct {
	destination string
	readOnly    bool
	volumeName  string
	volume      bool
}
type mountSnapshot struct {
	mounts           []observedMount
	healthy          bool
	image            string
	modelEnvironment map[string]string
	observedAt       time.Time
	reason           string
}

type executionContainerObservation struct {
	id      string
	stopped bool
}

// A list result is a discovery hint. Stop and release evidence must be based on
// a fresh inspect of every returned immutable ID, including legacy/exited rows.
func inspectExecutionContainers(ctx context.Context, client mountClient, rows []container.Summary, identity executionContainerTarget) ([]executionContainerObservation, error) {
	observations := make([]executionContainerObservation, 0, len(rows))
	for _, row := range rows {
		if row.ID == "" || !identity.matches(row.Labels) {
			return nil, errors.New("container_identity_changed")
		}
		actual, err := client.ContainerInspect(ctx, row.ID)
		if err != nil || actual.ContainerJSONBase == nil || actual.ID != row.ID || actual.Config == nil || actual.State == nil || !identity.matches(actual.Config.Labels) {
			return nil, errors.New("container_identity_changed")
		}
		state := actual.State
		stopped := (state.Status == "exited" || state.Status == "dead") && !state.Running && !state.Paused && !state.Restarting
		observations = append(observations, executionContainerObservation{id: row.ID, stopped: stopped})
	}
	return observations, nil
}

func inspectExecutionContainersStopped(ctx context.Context, client mountClient, rows []container.Summary, identity executionContainerTarget) (bool, error) {
	observations, err := inspectExecutionContainers(ctx, client, rows, identity)
	if err != nil {
		return false, err
	}
	for _, observation := range observations {
		if !observation.stopped {
			return false, nil
		}
	}
	return true, nil
}

// Validation additionally proves the requested immutable image and approved
// model parameters belong to the unique running registered task. These private
// inspect fields are never included in a browser response.
func modelRuntimeVerifier(provider edgestack.MountProvider) func(context.Context, edgestack.Task, edgestack.ModelPackage) error {
	return func(ctx context.Context, task edgestack.Task, model edgestack.ModelPackage) error {
		p, ok := provider.(*mountProvider)
		if !ok {
			return errors.New("runtime_image_unconfirmed")
		}
		ctx, cancel := context.WithTimeout(ctx, mountQueryTimeout)
		defer cancel()
		snapshot := p.snapshot(ctx, task)
		if snapshot.reason != "" || snapshot.observedAt.IsZero() {
			return errors.New("runtime_image_unconfirmed")
		}
		expected, err := reference.ParseNormalizedNamed(model.Image)
		if err != nil {
			return errors.New("runtime_image_unconfirmed")
		}
		actual, err := reference.ParseNormalizedNamed(snapshot.image)
		if err != nil {
			return errors.New("runtime_image_unconfirmed")
		}
		wanted, wantedOK := expected.(reference.Canonical)
		running, runningOK := actual.(reference.Canonical)
		if !wantedOK || !runningOK || wanted.Name() != running.Name() || wanted.Digest() != running.Digest() {
			return errors.New("runtime_image_unconfirmed")
		}
		for key, value := range model.Environment {
			if got, ok := snapshot.modelEnvironment[key]; !ok || got != value {
				return errors.New("runtime_model_parameters_unconfirmed")
			}
		}
		return nil
	}
}

type mountCacheKey struct {
	target   edgestack.Target
	revision int
}
type mountCacheEntry struct {
	done     chan struct{}
	snapshot mountSnapshot
	expires  time.Time
}
type mountProvider struct {
	newClient      func(context.Context, edgestack.Task) (mountClient, error)
	executionScope string
	stackName      func(int) (string, error)
	now            func() time.Time
	mu             sync.Mutex
	cache          map[mountCacheKey]*mountCacheEntry
}

func newMountProvider(store dataservices.DataStore, factory *dockerclient.ClientFactory, scope string) edgestack.MountProvider {
	if factory == nil {
		return nil
	}
	return &mountProvider{executionScope: scope, stackName: func(id int) (string, error) {
		stack, err := store.EdgeStack().EdgeStack(portainer.EdgeStackID(id))
		if err != nil {
			return "", err
		}
		return stack.Name, nil
	}, now: time.Now, cache: make(map[mountCacheKey]*mountCacheEntry), newClient: func(ctx context.Context, task edgestack.Task) (mountClient, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		endpoint, err := store.Endpoint().Endpoint(portainer.EndpointID(task.Target.EndpointID))
		if err != nil {
			return nil, err
		}
		if endpoint.Type != portainer.EdgeAgentOnDockerEnvironment {
			return nil, errors.New("unsupported mount target")
		}
		timeout := mountQueryTimeout
		// The original factory signs requests and opens the existing Edge
		// reverse tunnel. Its tunnel-open call is not context-aware; callers
		// below still return within two seconds, sharing one in-flight query.
		return factory.CreateClient(endpoint, "", &timeout)
	}}
}

func (p *mountProvider) Verify(ctx context.Context, task edgestack.Task, requests []edgestack.MountRequest) []edgestack.MountVerification {
	ctx, cancel := context.WithTimeout(ctx, mountQueryTimeout)
	defer cancel()
	snapshot := p.snapshot(ctx, task)
	out := make([]edgestack.MountVerification, len(requests))
	for i, request := range requests {
		result := edgestack.MountVerification{Reason: snapshot.reason}
		if !path.IsAbs(request.ContainerPath) || path.Clean(request.ContainerPath) != request.ContainerPath {
			result.Reason = "invalid_container_path"
			out[i] = result
			continue
		}
		if snapshot.reason != "" {
			out[i] = result
			continue
		}
		matches := []observedMount{}
		for _, mount := range snapshot.mounts {
			if mount.destination == request.ContainerPath {
				matches = append(matches, mount)
			}
		}
		if len(matches) == 0 {
			result.Reason = "mount_not_found"
		} else if len(matches) > 1 {
			result.Reason = "ambiguous_mount"
		} else {
			actual := matches[0]
			if request.ExpectedReadOnly != nil && actual.readOnly != *request.ExpectedReadOnly {
				result.Reason = "mount_read_only_mismatch"
			} else if request.VolumeName != "" && (!actual.volume || actual.volumeName != request.VolumeName) {
				result.Reason = "mount_volume_mismatch"
			} else {
				result.Verified = true
				result.ObservedAt = snapshot.observedAt
				result.Reason = ""
			}
		}
		out[i] = result
	}
	return out
}

func (p *mountProvider) ObserveRuntime(ctx context.Context, task edgestack.Task) edgestack.RuntimeObservation {
	ctx, cancel := context.WithTimeout(ctx, mountQueryTimeout)
	defer cancel()
	snapshot := p.snapshot(ctx, task)
	if snapshot.reason != "" || snapshot.observedAt.IsZero() {
		return edgestack.RuntimeObservation{State: "unknown"}
	}
	return edgestack.RuntimeObservation{State: "running", ObservedAt: snapshot.observedAt}
}

func (p *mountProvider) snapshot(ctx context.Context, task edgestack.Task) mountSnapshot {
	if task.Target.EndpointID <= 0 || task.Target.StackID <= 0 || task.Target.ServiceID == "" {
		return mountSnapshot{reason: "mount_target_unavailable"}
	}
	if p.executionScope != "" && p.stackName != nil {
		name, err := p.stackName(task.Target.StackID)
		if err != nil || name == "" {
			return mountSnapshot{reason: "container_scope_target_unavailable"}
		}
		task.Target.StackName = name
	}
	key := mountCacheKey{task.Target, task.Revision}
	p.mu.Lock()
	entry, exists := p.cache[key]
	if exists {
		select {
		case <-entry.done:
			if !p.now().Before(entry.expires) {
				exists = false
			}
		default:
		}
	}
	if !exists {
		// Bound stale cache growth without evicting in-flight tunnel queries.
		for cachedKey, cached := range p.cache {
			select {
			case <-cached.done:
				if !p.now().Before(cached.expires) {
					delete(p.cache, cachedKey)
				}
			default:
			}
		}
		entry = &mountCacheEntry{done: make(chan struct{})}
		p.cache[key] = entry
		go func() {
			queryCtx, cancel := context.WithTimeout(context.Background(), mountQueryTimeout)
			defer cancel()
			snapshot := p.inspect(queryCtx, task)
			p.mu.Lock()
			entry.snapshot = snapshot
			entry.expires = p.now().Add(mountCacheTTL)
			close(entry.done)
			p.mu.Unlock()
		}()
	}
	p.mu.Unlock()
	select {
	case <-entry.done:
		p.mu.Lock()
		snapshot := entry.snapshot
		p.mu.Unlock()
		return snapshot
	case <-ctx.Done():
		return mountSnapshot{reason: "mount_query_timeout"}
	}
}

func (p *mountProvider) inspect(ctx context.Context, task edgestack.Task) mountSnapshot {
	identity, err := p.containerTarget(task)
	if err != nil {
		return mountSnapshot{reason: "container_scope_target_unavailable"}
	}
	client, err := p.newClient(ctx, task)
	if err != nil {
		return mountSnapshot{reason: mountErrorReason(ctx)}
	}
	if client == nil {
		return mountSnapshot{reason: "mount_query_unavailable"}
	}
	defer client.Close()
	if ctx.Err() != nil {
		return mountSnapshot{reason: "mount_query_timeout"}
	}
	query := identity.filters()
	query.Add("status", "running")
	options := container.ListOptions{Filters: query}
	rows, err := client.ContainerList(ctx, options)
	if err != nil {
		return mountSnapshot{reason: mountErrorReason(ctx)}
	}
	var running []container.Summary
	for _, row := range rows {
		if identity.matches(row.Labels) && row.State == "running" {
			running = append(running, row)
		}
	}
	if len(running) == 0 {
		return mountSnapshot{reason: "no_running_container"}
	}
	if len(running) != 1 {
		return mountSnapshot{reason: "multiple_running_containers"}
	}
	actual, err := client.ContainerInspect(ctx, running[0].ID)
	if err != nil {
		return mountSnapshot{reason: mountErrorReason(ctx)}
	}
	if actual.ContainerJSONBase == nil || actual.ID != running[0].ID || actual.Config == nil || !identity.matches(actual.Config.Labels) {
		return mountSnapshot{reason: "container_identity_mismatch"}
	}
	if actual.State == nil || !actual.State.Running {
		return mountSnapshot{reason: "container_not_running"}
	}
	if ctx.Err() != nil {
		return mountSnapshot{reason: "mount_query_timeout"}
	}
	result := mountSnapshot{healthy: actual.State.Health != nil && actual.State.Health.Status == "healthy", observedAt: p.now().UTC(), image: actual.Config.Image, modelEnvironment: make(map[string]string), mounts: make([]observedMount, 0, len(actual.Mounts))}
	for _, item := range actual.Config.Env {
		key, value, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		for _, allowed := range task.ModelEnvKeys {
			if key == allowed {
				result.modelEnvironment[key] = value
				break
			}
		}
	}
	for _, point := range actual.Mounts {
		result.mounts = append(result.mounts, observedMount{destination: path.Clean(point.Destination), readOnly: !point.RW, volumeName: point.Name, volume: point.Type == mount.TypeVolume})
	}
	return result
}
func mountErrorReason(ctx context.Context) string {
	if ctx.Err() != nil {
		return "mount_query_timeout"
	}
	return "mount_query_unavailable"
}

// Rollout observation requires both the immutable model image and a healthy
// Docker health check. Runtime model receipts are verified separately by the
// business operation; a running process alone does not pass this gate.
func deliveryHealthObserver(provider edgestack.MountProvider) func(context.Context, edgestack.Task, edgestack.ModelPackage) error {
	p, ok := provider.(*mountProvider)
	if !ok {
		return nil
	}
	verify := modelRuntimeVerifier(provider)
	return func(ctx context.Context, task edgestack.Task, model edgestack.ModelPackage) error {
		if err := verify(ctx, task, model); err != nil {
			return err
		}
		snapshot := p.snapshot(ctx, task)
		if !snapshot.healthy {
			return errors.New("runtime_health_unconfirmed")
		}
		return nil
	}
}
