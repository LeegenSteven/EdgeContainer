package edgestackbusiness

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/dataservices"
	"github.com/portainer/portainer/api/edgestack"
	"github.com/portainer/portainer/pkg/libstack"
)

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
	task, group, err := a.validationHandoffTarget(job)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err = a.stopResidentForHandoff(r.Context(), job, group, body.Claim); err != nil {
		status := http.StatusConflict
		if err.Error() == "validation_task_stop_policy_required" {
			status = http.StatusForbidden
		}
		http.Error(w, err.Error(), status)
		return
	}
	// Repeat the group, target, policy and claim checks after tunnel/authority I/O.
	// The native executor still has to acquire its own fenced shared lease.
	if err = a.proveValidationStopped(r.Context(), task, group); err != nil {
		http.Error(w, "validation_stop_unconfirmed", http.StatusConflict)
		return
	}
	if _, err = a.service.DeliveryValidateExecutionStopClaim(job.ID, body.Claim); err != nil {
		http.Error(w, "validation_task_stop_policy_required", http.StatusForbidden)
		return
	}
	current, currentGroup, err := a.validationHandoffTarget(job)
	if err != nil || currentGroup != group || current.Target != task.Target || current.MetadataVersion != task.MetadataVersion || current.Revision != task.Revision {
		http.Error(w, "validation_resource_busy", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ready"})
}

func (a *deliveryExecutionAuthority) validationHandoffTarget(job edgestack.DeliveryJob) (edgestack.Task, string, error) {
	var empty edgestack.Task
	if err := a.service.VerifyDeliveryExecutionScope(a.executionScope); err != nil {
		return empty, "", errors.New("execution_scope_identity_changed")
	}
	group := a.devices[job.ValidationDeviceID]
	if group == "" {
		return empty, "", errors.New("device_resource_group_unconfigured")
	}
	var devices []string
	for device, mapped := range a.devices {
		if mapped == group {
			devices = append(devices, device)
		}
	}
	tasks, err := a.service.DeliveryExecutionGroupTasks(devices)
	if err != nil || len(tasks) != 1 || tasks[0].ID != job.ValidationTaskID || tasks[0].DeviceID != job.ValidationDeviceID || !tasks[0].TestTask {
		return empty, "", errors.New("validation_resource_contains_other_task")
	}
	if busy, err := a.service.DeliveryExecutionTaskBusy(tasks[0].ID); err != nil || busy {
		return empty, "", errors.New("validation_resource_busy")
	}
	return tasks[0], group, nil
}

func (a *deliveryExecutionAuthority) stopResidentForHandoff(ctx context.Context, job edgestack.DeliveryJob, group string, claim edgestack.DeliveryClaim) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	lease, known, err := a.service.DeliveryExecutionLease(group)
	if err != nil {
		return errors.New("execution_registry_unavailable")
	}
	if !known || lease.Released {
		return nil // Read-only convergence and Docker proof follow on both paths.
	}
	if lease.ControlEpoch != a.service.DeliveryControlEpoch() || !time.Now().Before(lease.ExpiresAt) {
		return errors.New("execution_epoch_changed")
	}
	authorize := func() error {
		if _, err := a.service.DeliveryValidateExecutionStopClaim(job.ID, claim); err != nil || !a.service.DeliveryValidationStopPolicyAllowed(job) {
			return errors.New("validation_task_stop_policy_required")
		}
		_, _, err := a.validationHandoffTarget(job)
		return err
	}
	if err := authorize(); err != nil {
		return err
	}
	// A re-claimed job can encounter a different resident lease. Never replay
	// a stop plan from an earlier occupancy against the new fence.
	id := "handoff-" + job.ID + "-" + lease.LeaseID
	if lease.ActiveOperationID == "handoff-"+job.ID {
		id = lease.ActiveOperationID // resume a pending stop written by an older Server
	}
	plan, err := a.service.DeliveryExecutionStopPlan(id)
	if err != nil {
		plan, err = a.previewExecutionStop(ctx, group, job.ResolverUserID)
		if err == nil {
			plan.ID = id
			err = a.service.DeliverySaveExecutionStopPlan(plan)
		}
	}
	if err != nil || plan.ControlEpoch != lease.ControlEpoch || plan.LeaseID != lease.LeaseID || len(plan.Targets) != 1 || plan.Targets[0].TaskID != job.ValidationTaskID {
		return errors.New("validation_resource_busy")
	}
	if err := a.confirmExecutionStopGuarded(ctx, &plan, authorize); err != nil {
		return errors.New("validation_stop_unconfirmed")
	}
	return nil
}

type validationStopEvidence struct {
	project string
	version string
	leaseID string
	fence   uint64
}

// Either this exact version has converged to zero replicas, or a durable
// acknowledged stop superseded its execution grant. A running one-shot
// service which merely reported Completed is insufficient.
func (a *deliveryExecutionAuthority) validationStopState(task edgestack.Task, group string) (validationStopEvidence, error) {
	var evidence validationStopEvidence
	if a.dispatcher == nil || a.mounts == nil || a.mounts.executionScope != a.executionScope {
		return evidence, errors.New("validation_inspection_unavailable")
	}
	lease, known, err := a.service.DeliveryExecutionLease(group)
	if err != nil || (known && !lease.Released) {
		return evidence, errors.New("validation_resource_busy")
	}
	var plan edgestack.DeliveryExecutionStopPlan
	if known {
		evidence.leaseID, evidence.fence = lease.LeaseID, lease.FencingToken
		// A manual stop may leave replicas=1 after its grant was superseded.
		// Its released receipt applies only to the exact stack version it stopped.
		plan, _ = a.service.DeliveryExecutionStopPlan(lease.ActiveOperationID)
	}
	err = a.store.ViewTx(func(tx dataservices.DataStoreTx) error {
		stack, content, err := a.dispatcher.target(tx, task)
		if err != nil {
			return err
		}
		if stack.Name == "" || stack.Version < 1 || (task.Target.StackName != "" && task.Target.StackName != stack.Name) {
			return errors.New("validation_target_changed")
		}
		root, err := serviceNode(content, task.Target.ServiceID)
		if err != nil {
			return err
		}
		evidence.project = libstack.EdgeStackProjectName(a.executionScope, stack.Name)
		evidence.version = version(stack, content)
		// The release acknowledgement is saved on the lease before updating
		// the plan's display status. A restart between those writes is safe.
		if known && plan.Stopped && plan.ID == lease.ActiveOperationID && plan.LeaseID == lease.LeaseID && plan.ResourceGroup == group && plan.ControlEpoch == lease.ControlEpoch {
			for _, target := range plan.Targets {
				if target.TaskID == task.ID && target.StackID == int(stack.ID) && target.Version == stack.Version {
					return nil
				}
			}
		}
		service := mappingValue(mappingValue(root.Content[0], "services"), task.Target.ServiceID)
		count, err := validationServiceScale(service)
		if err != nil || count != 0 || mappingValue(service, "extends") != nil || mappingValue(root.Content[0], "include") != nil {
			return errors.New("validation_zero_replicas_required")
		}
		status, err := tx.EdgeStackStatus().Read(stack.ID, portainer.EndpointID(task.Target.EndpointID))
		if err != nil {
			return err
		}
		// A historic Completed or a delivery ACK alone is not current convergence.
		if currentDeploymentCompleted(status, stack.Version) {
			return nil
		}
		return errors.New("validation_version_not_completed")
	})
	return evidence, err
}

func currentDeploymentCompleted(status *portainer.EdgeStackStatusForEnv, version int) bool {
	if status == nil || version < 1 || len(status.Status) == 0 {
		return false
	}
	last := status.Status[len(status.Status)-1]
	if last.Version != version || last.Type != portainer.EdgeStackStatusCompleted {
		return false
	}
	for _, prior := range status.Status {
		if prior.Version == version && prior.Time > last.Time {
			return false
		}
	}
	return true
}

func inspectStoppedProject(ctx context.Context, client mountClient, identity executionContainerTarget) (bool, error) {
	if identity.project == "" {
		return false, errors.New("container_project_unavailable")
	}
	// Filtering by service or ownership would hide collisions within the project.
	query := container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", composeProjectLabel+"="+identity.project))}
	rows, err := client.ContainerList(ctx, query)
	if err != nil {
		return false, err
	}
	return inspectExecutionContainersStopped(ctx, client, rows, identity)
}

func (a *deliveryExecutionAuthority) proveValidationStopped(ctx context.Context, task edgestack.Task, group string) error {
	before, err := a.validationStopState(task, group)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	identity, err := a.mounts.containerTarget(task)
	if err != nil || (identity.scope != "" && identity.project != before.project) {
		return errors.New("container_scope_target_unavailable")
	}
	client, err := a.mounts.newClient(ctx, task)
	if err != nil || client == nil {
		return errors.New("container_inspection_unavailable")
	}
	defer client.Close()
	identity.project = before.project
	for i := 0; i < 2; i++ {
		stopped, err := inspectStoppedProject(ctx, client, identity)
		if err != nil || !stopped {
			return errors.New("validation_stop_unconfirmed")
		}
		if i == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	after, err := a.validationStopState(task, group)
	if err != nil || before != after || ctx.Err() != nil {
		return errors.New("validation_state_changed")
	}
	return nil
}
