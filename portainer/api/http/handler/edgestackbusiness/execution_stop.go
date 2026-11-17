package edgestackbusiness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/edgestack"
)

type executionContainerStopper interface {
	ContainerStop(context.Context, string, container.StopOptions) error
}

func (a *deliveryExecutionAuthority) adminExecutionStop(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.TrimRight(r.URL.Path, "/") == "/api/edgestack/admin/execution-leases" {
		if _, err := a.service.DeliveryExecutionAdmin(r); err != nil {
			http.Error(w, "administrator_required", http.StatusForbidden)
			return
		}
		leases, err := a.service.DeliveryExecutionLeases()
		if err != nil {
			http.Error(w, "execution_registry_unavailable", 503)
			return
		}
		bindings, err := a.service.DeliveryExecutionBindings()
		if err != nil {
			http.Error(w, "execution_registry_unavailable", 503)
			return
		}
		rows := []map[string]any{}
		for _, lease := range leases {
			targets := []map[string]any{}
			canStop := !lease.Released
			for _, binding := range bindings {
				if binding.Grant.ResourceGroup == lease.ResourceGroup {
					task, e := a.service.DeliveryExecutionTask(binding.TaskID)
					if e != nil {
						canStop = false
						continue
					}
					canStop = canStop && task.TestTask
					targets = append(targets, map[string]any{"taskId": task.ID, "name": task.Name, "testTask": task.TestTask})
				}
			}
			status := "resident"
			if lease.Released {
				status = "released"
			} else if lease.ControlEpoch != a.service.DeliveryControlEpoch() || !time.Now().Before(lease.ExpiresAt) {
				status = "needs_review"
			}
			rows = append(rows, map[string]any{"resourceGroup": lease.ResourceGroup, "status": status, "targets": targets, "canStop": canStop && len(targets) > 0, "expiresAt": lease.ExpiresAt})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": rows})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	actor, err := a.service.DeliveryExecutionAdmin(r)
	if err != nil {
		http.Error(w, "administrator_required", http.StatusForbidden)
		return
	}
	var body struct {
		ResourceGroup string `json:"resourceGroup"`
		PreviewID     string `json:"previewId"`
		Confirm       bool   `json:"confirm"`
	}
	if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "invalid_stop_request", http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var plan edgestack.DeliveryExecutionStopPlan
	if strings.HasSuffix(r.URL.Path, "/stop-preview") {
		plan, err = a.previewExecutionStop(r.Context(), body.ResourceGroup, actor)
	} else if strings.HasSuffix(r.URL.Path, "/stop-confirm") || strings.HasSuffix(r.URL.Path, "/reconcile-stop") {
		if !body.Confirm {
			http.Error(w, "stop_confirmation_required", http.StatusBadRequest)
			return
		}
		plan, err = a.service.DeliveryExecutionStopPlan(body.PreviewID)
		if err == nil && (plan.ActorUserID != actor || plan.ControlEpoch != a.service.DeliveryControlEpoch() || !time.Now().Before(plan.ExpiresAt)) {
			err = errors.New("stop_preview_expired_or_foreign")
		}
		if err == nil && plan.Status != "released" {
			err = a.confirmExecutionStop(r.Context(), &plan)
		}
	} else {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		if plan.ID != "" && plan.Status != "preview" && plan.Status != "released" {
			plan.Status = "needs_review"
			_ = a.service.DeliverySaveExecutionStopPlan(plan)
		}
		http.Error(w, "execution_stop_unconfirmed: "+err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(plan)
}

func (a *deliveryExecutionAuthority) previewExecutionStop(ctx context.Context, group string, actor int) (edgestack.DeliveryExecutionStopPlan, error) {
	var plan edgestack.DeliveryExecutionStopPlan
	lease, known, err := a.service.DeliveryExecutionLease(group)
	if err != nil || !known || lease.Released {
		return plan, errors.New("resident_execution_lease_not_found")
	}
	plan.Recovery = lease.ControlEpoch != a.service.DeliveryControlEpoch() || !time.Now().Before(lease.ExpiresAt)
	if !plan.Recovery {
		if err = a.verifyLease(ctx, lease); err != nil {
			return plan, err
		}
	} else {
		view, e := a.call(ctx, http.MethodGet, "/v1/device-leases/"+url.PathEscape(lease.LeaseID), "", nil)
		if e != nil || view.LeaseID != lease.LeaseID || view.FencingTokens[group] != lease.FencingToken || view.OwnerAgent != "edgestack_server" || (view.Purpose != "deploy_write" && view.Purpose != "release") || (!strings.HasPrefix(view.JobID, "edge-execution:") && !strings.HasPrefix(view.JobID, "edge-recovery:")) {
			return plan, errors.New("execution_recovery_owner_unconfirmed")
		}
		plan.PreviousJobID = view.JobID
	}
	devices := []string{}
	for device, mapped := range a.devices {
		if mapped == group {
			devices = append(devices, device)
		}
	}
	groupTasks, e := a.service.DeliveryExecutionGroupTasks(devices)
	if e != nil {
		return plan, e
	}
	for _, task := range groupTasks {
		if !task.TestTask {
			return plan, errors.New("resource_group_contains_business_or_unresolved_target")
		}
	}
	rows, err := a.service.DeliveryExecutionBindings()
	if err != nil {
		return plan, err
	}
	for _, row := range rows {
		if row.Grant.ResourceGroup != group {
			continue
		}
		task, e := a.service.DeliveryExecutionTask(row.TaskID)
		if e != nil || !task.TestTask || task.Target.StackID != row.StackID {
			return plan, errors.New("resource_group_contains_business_or_unresolved_target")
		}
		if busy, e := a.service.DeliveryExecutionTaskBusy(task.ID); e != nil || (busy && !plan.Recovery) {
			return plan, errors.New("test_target_operation_still_active")
		}
		stack, e := a.store.EdgeStack().EdgeStack(portainer.EdgeStackID(row.StackID))
		if e != nil {
			return plan, e
		}
		plan.Targets = append(plan.Targets, edgestack.DeliveryExecutionStopTarget{TaskID: task.ID, StackID: row.StackID, Version: stack.Version})
	}
	if len(plan.Targets) == 0 {
		return plan, errors.New("no_managed_test_targets")
	}
	value := make([]byte, 24)
	if _, err = rand.Read(value); err != nil {
		return plan, err
	}
	plan.ID = "stop-" + hex.EncodeToString(value)
	plan.ActorUserID = actor
	plan.ControlEpoch = a.service.DeliveryControlEpoch()
	plan.ResourceGroup = group
	plan.LeaseID = lease.LeaseID
	plan.ExpiresAt = time.Now().Add(5 * time.Minute)
	plan.Status = "preview"
	return plan, a.service.DeliverySaveExecutionStopPlan(plan)
}

func (a *deliveryExecutionAuthority) confirmExecutionStop(ctx context.Context, plan *edgestack.DeliveryExecutionStopPlan) error {
	if a.mounts == nil {
		return errors.New("container_inspection_unavailable")
	}
	// Recheck the complete group at confirmation, since administrator metadata
	// can change after the preview. Recovery must never supersede a new business
	// target or claim stop evidence for an unreviewed managed stack.
	devices := []string{}
	for device, group := range a.devices {
		if group == plan.ResourceGroup {
			devices = append(devices, device)
		}
	}
	tasks, err := a.service.DeliveryExecutionGroupTasks(devices)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if !task.TestTask {
			return errors.New("resource_group_contains_business_or_unresolved_target")
		}
	}
	bindings, err := a.service.DeliveryExecutionBindings()
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if binding.Grant.ResourceGroup != plan.ResourceGroup {
			continue
		}
		reviewed := false
		for _, target := range plan.Targets {
			if target.TaskID == binding.TaskID && target.StackID == binding.StackID {
				reviewed = true
			}
		}
		if !reviewed {
			return errors.New("execution_stop_targets_changed")
		}
	}
	lease, known, err := a.service.DeliveryExecutionLease(plan.ResourceGroup)
	if err != nil || !known || (lease.LeaseID != plan.LeaseID && lease.ActiveOperationID != plan.ID) {
		return errors.New("resident_execution_lease_changed")
	}
	if lease.LeaseID != plan.LeaseID && lease.ActiveOperationID == plan.ID {
		plan.LeaseID = lease.LeaseID
		plan.Recovery = false
		if err = a.service.DeliverySaveExecutionStopPlan(*plan); err != nil {
			return err
		}
	}
	if plan.Recovery {
		view, e := a.call(ctx, http.MethodPost, "/v1/device-leases/"+url.PathEscape(plan.LeaseID)+"/takeover-release", plan.ID, map[string]any{"fencing_tokens": map[string]uint64{lease.ResourceGroup: lease.FencingToken}, "owner_agent": "edgestack_server", "job_id": "edge-recovery:" + plan.ID, "recovery_id": plan.ID, "previous_job_id": plan.PreviousJobID, "evidence": "administrator reviewed isolated validation targets; old control epoch fenced; container reconciliation follows"})
		if e != nil {
			return e
		}
		if view.LeaseID == "" || view.FencingTokens[lease.ResourceGroup] <= lease.FencingToken {
			return errors.New("execution_recovery_fence_unconfirmed")
		}
		// Persist the successor even when its ACK reports expiry, so a new
		// administrator preview can recover the actual outstanding occupancy.
		lease.LeaseID = view.LeaseID
		lease.FencingToken = view.FencingTokens[lease.ResourceGroup]
		lease.ControlEpoch = a.service.DeliveryControlEpoch()
		lease.ExpiresAt = view.ExpiresAt
		lease.AuthorityJobID = "edge-recovery:" + plan.ID
		lease.ActiveOperationID = plan.ID
		if err = a.service.DeliverySaveExecutionLease(lease); err != nil {
			return err
		}
		plan.LeaseID = lease.LeaseID
		plan.Recovery = false
		if err = a.service.DeliverySaveExecutionStopPlan(*plan); err != nil {
			return err
		}
		if view.Status != "active" || !time.Now().Before(view.ExpiresAt) {
			return errors.New("execution_recovery_successor_expired")
		}
	}
	if plan.Stopped {
		view, e := a.call(ctx, http.MethodGet, "/v1/device-leases/"+url.PathEscape(lease.LeaseID), "", nil)
		if e == nil && (view.Status == "released" || lease.Released) {
			lease.Released = true
			plan.Status = "released"
			if e = a.service.DeliverySaveExecutionLease(lease); e != nil {
				return e
			}
			return a.service.DeliverySaveExecutionStopPlan(*plan)
		}
	}
	if err = a.verifyLease(ctx, lease); err != nil {
		return err
	}
	// Supersede old Edge commands before touching Docker. Their periodic authority
	// check cancels in-flight commands; no later queued command can restart them.
	lease.ActiveOperationID = plan.ID
	if err = a.service.DeliverySaveExecutionLease(lease); err != nil {
		return err
	}
	plan.Status = "stopping"
	if err = a.service.DeliverySaveExecutionStopPlan(*plan); err != nil {
		return err
	}
	timer := time.NewTimer(4 * time.Second)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
	}
	for _, target := range plan.Targets {
		task, e := a.service.DeliveryExecutionTask(target.TaskID)
		if e != nil || !task.TestTask || task.Target.StackID != target.StackID {
			return errors.New("test_target_changed")
		}
		stack, e := a.store.EdgeStack().EdgeStack(portainer.EdgeStackID(target.StackID))
		if e != nil || stack.Version != target.Version {
			return errors.New("test_stack_version_changed")
		}
		if err = a.stopExecutionContainers(ctx, task, lease); err != nil {
			return err
		}
	}
	plan.Stopped = true
	if err = a.service.DeliverySaveExecutionStopPlan(*plan); err != nil {
		return err
	}
	view, err := a.call(ctx, http.MethodPost, "/v1/device-leases/"+url.PathEscape(lease.LeaseID)+"/release", "", map[string]any{"remote_stop_confirmed": true, "evidence": "administrator confirmed stop of isolated test tasks; all owned containers observed stopped; prior grants superseded"})
	if err != nil {
		return err
	}
	if view.LeaseID != lease.LeaseID || view.Status != "released" {
		return errors.New("execution_release_unconfirmed")
	}
	lease.Released = true
	plan.Status = "released"
	if err = a.service.DeliverySaveExecutionLease(lease); err != nil {
		return err
	}
	return a.service.DeliverySaveExecutionStopPlan(*plan)
}

func (a *deliveryExecutionAuthority) stopExecutionContainers(ctx context.Context, task edgestack.Task, lease edgestack.DeliveryExecutionLease) error {
	bounded, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	client, err := a.mounts.newClient(bounded, task)
	if err != nil {
		return err
	}
	defer client.Close()
	stopper, ok := client.(executionContainerStopper)
	if !ok {
		return errors.New("container_stop_unavailable")
	}
	id := strconv.Itoa(task.Target.StackID)
	query := container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", edgeStackLabel+"="+id), filters.Arg("label", composeServiceLabel+"="+task.Target.ServiceID))}
	stoppedReads := 0
	for stoppedReads < 2 {
		if err = a.verifyLease(bounded, lease); err != nil {
			return err
		}
		rows, e := client.ContainerList(bounded, query)
		if e != nil {
			return e
		}
		active := false
		for _, row := range rows {
			if row.Labels[edgeStackLabel] != id || row.Labels[composeServiceLabel] != task.Target.ServiceID {
				return errors.New("container_identity_changed")
			}
			if row.State != "exited" && row.State != "dead" {
				active = true
			}
		}
		if !active {
			stoppedReads++
		} else {
			stoppedReads = 0
			for _, row := range rows {
				if row.Labels[edgeStackLabel] != id || row.Labels[composeServiceLabel] != task.Target.ServiceID {
					return errors.New("container_identity_changed")
				}
				if row.State == "exited" || row.State == "dead" {
					continue
				}
				timeout := 10
				if e = stopper.ContainerStop(bounded, row.ID, container.StopOptions{Timeout: &timeout}); e != nil {
					return e
				}
			}
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-bounded.Done():
			timer.Stop()
			return bounded.Err()
		case <-timer.C:
		}
	}
	return nil
}

func (a *deliveryExecutionAuthority) internalStopValidationTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := a.service.DeliveryAuthenticateWorker(r); err != nil {
		http.Error(w, "worker_authentication_required", http.StatusForbidden)
		return
	}
	var body struct {
		JobID string                  `json:"jobId"`
		Claim edgestack.DeliveryClaim `json:"claim"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
		http.Error(w, "invalid_stop_request", 400)
		return
	}
	job, err := a.service.DeliveryValidateExecutionStopClaim(body.JobID, body.Claim)
	if err != nil {
		http.Error(w, "validation_task_stop_policy_required", http.StatusForbidden)
		return
	}
	task, err := a.service.DeliveryExecutionTask(job.ValidationTaskID)
	if err != nil || !task.TestTask || task.DeviceID != job.ValidationDeviceID {
		http.Error(w, "isolated_validation_task_required", 409)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	group := a.devices[task.DeviceID]
	if group == "" {
		http.Error(w, "device_resource_group_unconfigured", 409)
		return
	}
	lease, known, err := a.service.DeliveryExecutionLease(group)
	if err != nil {
		http.Error(w, "execution_registry_unavailable", 503)
		return
	}
	if known && !lease.Released {
		if lease.ControlEpoch != a.service.DeliveryControlEpoch() || !time.Now().Before(lease.ExpiresAt) {
			http.Error(w, "execution_epoch_changed", http.StatusConflict)
			return
		}
		devices := []string{}
		for device, mapped := range a.devices {
			if mapped == group {
				devices = append(devices, device)
			}
		}
		tasks, e := a.service.DeliveryExecutionGroupTasks(devices)
		if e != nil {
			http.Error(w, "execution_registry_unavailable", 503)
			return
		}
		for _, candidate := range tasks {
			if candidate.ID != task.ID {
				http.Error(w, "validation_resource_contains_other_task", 409)
				return
			}
		}
		if !a.service.DeliveryValidationStopPolicyAllowed(job) {
			http.Error(w, "validation_task_stop_policy_required", http.StatusForbidden)
			return
		}
		plan, e := a.service.DeliveryExecutionStopPlan("handoff-" + job.ID)
		if e != nil {
			plan, e = a.previewExecutionStop(r.Context(), group, job.ResolverUserID)
			if e == nil {
				plan.ID = "handoff-" + job.ID
				e = a.service.DeliverySaveExecutionStopPlan(plan)
			}
		}
		if e != nil {
			http.Error(w, "validation_resource_busy", 409)
			return
		}
		if plan.ControlEpoch != a.service.DeliveryControlEpoch() {
			http.Error(w, "execution_epoch_changed", http.StatusConflict)
			return
		}
		for _, target := range plan.Targets {
			if target.TaskID != job.ValidationTaskID {
				http.Error(w, "validation_resource_contains_other_task", 409)
				return
			}
		}
		if e = a.confirmExecutionStop(r.Context(), &plan); e != nil {
			http.Error(w, "validation_stop_unconfirmed", 409)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ready"})
}
