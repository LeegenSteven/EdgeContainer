package edgestack

import (
	"context"
	"net/http"
	"path"
	"time"

	bolt "go.etcd.io/bbolt"
)

type mountProjection struct {
	task      Task
	public    map[string]any
	locations []map[string]any
	indexes   []int
	requests  []MountRequest
}
type mountProjectionResult struct {
	index   int
	checks  []MountVerification
	runtime RuntimeObservation
}

// enrichMountLocations runs after the business read transaction has closed.
// All network queries share a two-second projection budget and are bounded to
// four concurrent tasks; the provider shares/caches its actual Docker reads.
func (s *Service) enrichMountLocations(r *http.Request, devices []any) error {
	if s.opts.MountProvider == nil {
		return nil
	}
	runtimeProvider, hasRuntime := s.opts.MountProvider.(RuntimeProvider)
	projections := []mountProjection{}
	err := s.db.View(func(tx *bolt.Tx) error {
		for _, item := range devices {
			device := item.(map[string]any)
			if _, err := s.authorizedDevice(r, tx, device["id"].(string)); err != nil {
				return err
			}
			for _, raw := range device["tasks"].([]any) {
				public := raw.(map[string]any)
				task, err := s.authorizedTask(r, tx, public["id"].(string))
				if err != nil {
					return err
				}
				projection := mountProjection{task: task, public: public, locations: public["dataLocations"].([]map[string]any)}
				for i, location := range projection.locations {
					location["verificationReason"] = "mount_path_not_registered"
					kind, _ := location["kind"].(string)
					if kind == "server" {
						location["verificationReason"] = "server_managed_records"
						continue
					}
					containerPath, ok := location["containerPath"].(string)
					if !ok || containerPath == "" {
						continue
					}
					if !path.IsAbs(containerPath) || path.Clean(containerPath) != containerPath {
						location["verificationReason"] = "invalid_container_path"
						continue
					}
					request := MountRequest{ContainerPath: containerPath}
					if value, exists := location["expectedReadOnly"]; exists {
						readOnly, ok := value.(bool)
						if !ok {
							location["verificationReason"] = "invalid_mount_expectation"
							continue
						}
						request.ExpectedReadOnly = &readOnly
					}
					if value, exists := location["volumeName"]; exists {
						name, ok := value.(string)
						if !ok || name == "" {
							location["verificationReason"] = "invalid_mount_expectation"
							continue
						}
						request.VolumeName = name
					}
					projection.requests = append(projection.requests, request)
					projection.indexes = append(projection.indexes, i)
				}
				if len(projection.requests) > 0 || hasRuntime {
					projections = append(projections, projection)
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(projections) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	results := make(chan mountProjectionResult, len(projections))
	slots := make(chan struct{}, 4)
	for index, projection := range projections {
		go func() {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			var runtime RuntimeObservation
			if hasRuntime {
				runtime = runtimeProvider.ObserveRuntime(ctx, projection.task)
			}
			var checks []MountVerification
			if len(projection.requests) > 0 {
				checks = s.opts.MountProvider.Verify(ctx, projection.task, projection.requests)
			}
			results <- mountProjectionResult{index: index, checks: checks, runtime: runtime}
		}()
	}
	remaining := len(projections)
	done := make([]bool, len(projections))
	for remaining > 0 {
		select {
		case result := <-results:
			done[result.index] = true
			projection := projections[result.index]
			if result.runtime.State == "running" && !result.runtime.ObservedAt.IsZero() {
				projection.public["runtimeState"] = "running"
				projection.public["runtimeObservedAt"] = result.runtime.ObservedAt
			}
			for i, index := range projection.indexes {
				location := projection.locations[index]
				location["verificationReason"] = "mount_query_unavailable"
				if i >= len(result.checks) {
					continue
				}
				check := result.checks[i]
				if check.Verified && !check.ObservedAt.IsZero() {
					location["verification"] = "verified"
					location["observedAt"] = check.ObservedAt
					delete(location, "verificationReason")
				} else if check.Reason != "" {
					location["verificationReason"] = check.Reason
				}
			}
			remaining--
		case <-ctx.Done():
			for i, projection := range projections {
				if done[i] {
					continue
				}
				for _, index := range projection.indexes {
					location := projection.locations[index]
					location["verificationReason"] = "mount_query_timeout"
				}
			}
			remaining = 0
		}
	}
	// The result and all other device/task data must still be authorized if
	// a session or business/Portainer permission changed during the query.
	return s.db.View(func(tx *bolt.Tx) error {
		for _, item := range devices {
			device := item.(map[string]any)
			if _, err := s.authorizedDevice(r, tx, device["id"].(string)); err != nil {
				return err
			}
			for _, raw := range device["tasks"].([]any) {
				if _, err := s.authorizedTask(r, tx, raw.(map[string]any)["id"].(string)); err != nil {
					return err
				}
			}
		}
		for _, projection := range projections {
			task, err := s.authorizedTask(r, tx, projection.task.ID)
			if err != nil {
				return err
			}
			if task.Revision != projection.task.Revision || task.Target != projection.task.Target {
				return fail(409, "task_changed_refresh_required")
			}
		}
		return nil
	})
}
