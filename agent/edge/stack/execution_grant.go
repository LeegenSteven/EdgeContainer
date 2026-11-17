package stack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/portainer/agent/edge/client"
	"github.com/portainer/portainer/api/edge"
	"github.com/portainer/portainer/pkg/libstack"
)

type managedTarget struct {
	Name          string `json:"name"`
	ResourceGroup string `json:"resourceGroup"`
}

type executionFence struct {
	Token   uint64 `json:"token"`
	LeaseID string `json:"leaseId"`
}

// Kept outside /tmp/edge_stacks: reset, successful removal and Agent restart
// must never turn a previously managed stack into an unguarded target.
type executionFenceStore struct {
	mu      sync.Mutex
	path    string
	loaded  bool
	err     error
	Targets map[string]managedTarget  `json:"targets"`
	Groups  map[string]executionFence `json:"groups"`
}

func (store *executionFenceStore) load() error {
	if store.loaded {
		return store.err
	}
	store.loaded = true
	store.Targets = make(map[string]managedTarget)
	store.Groups = make(map[string]executionFence)
	if store.path == "" {
		return nil
	}
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil {
		var saved struct {
			Targets map[string]managedTarget  `json:"targets"`
			Groups  map[string]executionFence `json:"groups"`
		}
		err = json.Unmarshal(data, &saved)
		if err == nil && (saved.Targets == nil || saved.Groups == nil) {
			err = errors.New("incomplete execution fence state")
		}
		if err == nil {
			store.Targets, store.Groups = saved.Targets, saved.Groups
		}
	}
	store.err = err
	return err
}

func (store *executionFenceStore) persist() error {
	if store.path == "" {
		return nil
	}
	data, err := json.Marshal(store)
	if err != nil {
		return err
	}
	directory := filepath.Dir(store.path)
	if err = os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".execution-fences-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(file.Name(), store.path); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// remember persists the managed marker before any side effect. A write failure
// is sticky for this process so no later retry can bypass an unsaved fence.
func (store *executionFenceStore) remember(payload *edge.StackPayload) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.load(); err != nil {
		return err
	}
	key := strconv.Itoa(payload.ID)
	previous, known := store.Targets[key]
	if !known && !payload.ManagedExecution && payload.ExecutionGrant == nil {
		return nil
	}
	payload.ManagedExecution = true
	next := previous
	if payload.Name != "" {
		next.Name = payload.Name
	}
	if payload.ExecutionGrant != nil {
		group := payload.ExecutionGrant.ResourceGroup
		if previous.ResourceGroup != "" && previous.ResourceGroup != group {
			return errors.New("managed_execution_group_changed")
		}
		next.ResourceGroup = group
	}
	if !known || previous != next {
		store.Targets[key] = next
		store.err = store.persist()
	}
	return store.err
}

func (store *executionFenceStore) check(grant edge.ExecutionGrant, save bool) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.load(); err != nil {
		return err
	}
	previous := store.Groups[grant.ResourceGroup]
	if previous.Token > grant.FencingToken || (previous.Token == grant.FencingToken && previous.LeaseID != grant.LeaseID) {
		return errors.New("managed_execution_stale_fence")
	}
	if save && grant.FencingToken > previous.Token {
		store.Groups[grant.ResourceGroup] = executionFence{Token: grant.FencingToken, LeaseID: grant.LeaseID}
		store.err = store.persist()
	}
	return store.err
}

func (store *executionFenceStore) rejectsNormalStack(name string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.load(); err != nil {
		return err
	}
	for _, target := range store.Targets {
		if target.Name == name || "edge_"+target.Name == name {
			return errors.New("managed_stack_requires_execution_grant")
		}
	}
	return nil
}

func (manager *StackManager) rememberManagedPayload(payload *edge.StackPayload) error {
	// Managers instantiated directly by legacy tests have no persistent store.
	// Production constructors always provide the durable state directory.
	if manager.executionFences == nil {
		manager.executionFences = &executionFenceStore{}
	}
	return manager.executionFences.remember(payload)
}

func (manager *StackManager) verifyManagedExecution(ctx context.Context, stack *edgeStack, action string) error {
	payload := stack.StackPayload
	if err := manager.rememberManagedPayload(&payload); err != nil {
		return err
	}
	if !payload.ManagedExecution {
		return nil
	}
	if manager.engineType != EngineTypeDockerStandalone || payload.SupportRelativePath || payload.EdgeUpdateID != 0 || payload.HelmConfig.ChartPath != "" || payload.HelmConfig.ChartURL != "" {
		return errors.New("managed_execution_requires_independent_compose_service")
	}
	if payload.ExecutionGrant == nil {
		return errors.New("managed_execution_grant_required")
	}
	grant := *payload.ExecutionGrant
	if err := grant.Validate(payload.Version, action, time.Now()); err != nil {
		return err
	}
	if err := manager.executionFences.check(grant, false); err != nil {
		return err
	}
	verifier, ok := manager.portainerClient.(client.ExecutionGrantVerifier)
	if !ok {
		return errors.New("managed_execution_verifier_required")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := verifier.VerifyExecutionGrant(checkCtx, payload.ID, payload.Version, action, grant); err != nil {
		return err
	}
	return manager.executionFences.check(grant, true)
}

// Start only after synchronous authority verification. Cancellation reaches the
// deployer's command context if the lease expires or a later check fails.
func (manager *StackManager) managedExecutionContext(ctx context.Context, stack *edgeStack, action string) (context.Context, func(error) error, error) {
	copyOfStack := *stack
	stack = &copyOfStack
	if stack.ExecutionGrant != nil {
		grant := *stack.ExecutionGrant
		stack.ExecutionGrant = &grant
	}
	if err := manager.rememberManagedPayload(&stack.StackPayload); err != nil {
		return nil, nil, err
	}
	if err := manager.verifyManagedExecution(ctx, stack, action); err != nil {
		return nil, nil, err
	}
	if !stack.ManagedExecution {
		return ctx, func(err error) error { return err }, nil
	}
	guarded, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-guarded.Done():
				return
			case <-ticker.C:
				if err := manager.verifyManagedExecution(guarded, stack, action); err != nil {
					cancel(fmt.Errorf("managed execution authority lost: %w", err))
					return
				}
			}
		}
	}()
	return libstack.WithExecutionContext(guarded), func(result error) error {
		cause := context.Cause(guarded)
		cancel(context.Canceled)
		<-done
		if cause != nil {
			return errors.Join(result, cause)
		}
		if err := manager.verifyManagedExecution(ctx, stack, action); err != nil {
			return errors.Join(result, err)
		}
		return result
	}, nil
}
