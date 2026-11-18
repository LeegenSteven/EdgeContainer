package edgestack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/portainer/portainer/api/edge"
	bolt "go.etcd.io/bbolt"
)

type DeliveryExecutionAuthority interface {
	Preview(context.Context, int) error
	Prepare(context.Context, string, Task, int, string) error
	Payload(context.Context, int, int) (bool, *edge.ExecutionGrant, error)
	Verify(context.Context, int, int, string, edge.ExecutionGrant) error
	ObserveCapability(context.Context, int, string, string) error
	ExecutionScope() string
	NativeGuard(http.Handler) http.Handler
	Run(context.Context)
}

func (s *Service) SetDeliveryExecutionAuthority(authority DeliveryExecutionAuthority) {
	s.opts.DeliveryExecutionAuthority = authority
}
func (s *Service) DeliveryExecutionAuthority() DeliveryExecutionAuthority {
	return s.opts.DeliveryExecutionAuthority
}

const executionBindingsBucket = "delivery-execution-grants"
const executionLeasesBucket = "delivery-execution-leases"

type deliveryOperationContext struct{}

func WithDeliveryOperation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, deliveryOperationContext{}, id)
}
func DeliveryOperationID(ctx context.Context) string {
	id, _ := ctx.Value(deliveryOperationContext{}).(string)
	return id
}

type DeliveryExecutionBinding struct {
	JobID         string              `json:"jobId,omitempty"`
	JobClaimEpoch int                 `json:"jobClaimEpoch,omitempty"`
	EndpointID    int                 `json:"endpointId"`
	StackID       int                 `json:"stackId"`
	DeviceID      string              `json:"deviceId"`
	TaskID        string              `json:"taskId"`
	Grant         edge.ExecutionGrant `json:"grant"`
}

type DeliveryExecutionLease struct {
	AuthorityJobID    string    `json:"authorityJobId,omitempty"`
	Released          bool      `json:"released"`
	ResourceGroup     string    `json:"resourceGroup"`
	LeaseID           string    `json:"leaseId"`
	FencingToken      uint64    `json:"fencingToken"`
	ControlEpoch      string    `json:"controlEpoch"`
	ExpiresAt         time.Time `json:"expiresAt"`
	ActiveOperationID string    `json:"activeOperationId"`
}

func (s *Service) DeliveryExecutionBinding(endpointID, stackID int) (DeliveryExecutionBinding, bool, error) {
	var value DeliveryExecutionBinding
	known := false
	err := s.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte(executionBindingsBucket)) == nil {
			return nil
		}
		var e error
		value, e = read[DeliveryExecutionBinding](tx, executionBindingsBucket, fmt.Sprintf("%d/%d", endpointID, stackID))
		if e == errNotFound {
			return nil
		}
		known = e == nil
		return e
	})
	return value, known, err
}

func (s *Service) DeliveryExecutionLease(group string) (DeliveryExecutionLease, bool, error) {
	var value DeliveryExecutionLease
	known := false
	err := s.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte(executionLeasesBucket)) == nil {
			return nil
		}
		var e error
		value, e = read[DeliveryExecutionLease](tx, executionLeasesBucket, group)
		if e == errNotFound {
			return nil
		}
		known = e == nil
		return e
	})
	return value, known, err
}

func (s *Service) DeliverySaveExecutionLease(value DeliveryExecutionLease) error {
	if value.ResourceGroup == "" || value.LeaseID == "" || value.FencingToken == 0 || value.ControlEpoch != s.deliveryEpoch {
		return fail(409, "invalid_execution_lease")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(executionLeasesBucket)); err != nil {
			return err
		}
		old, err := read[DeliveryExecutionLease](tx, executionLeasesBucket, value.ResourceGroup)
		if err == nil && (old.FencingToken > value.FencingToken || (old.FencingToken == value.FencingToken && old.LeaseID != value.LeaseID)) {
			return fail(409, "execution_fence_changed")
		}
		return put(tx, executionLeasesBucket, value.ResourceGroup, value)
	})
}

func (s *Service) DeliveryExecutionLeases() ([]DeliveryExecutionLease, error) {
	var rows []DeliveryExecutionLease
	err := s.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte(executionLeasesBucket)) == nil {
			return nil
		}
		var e error
		rows, e = list[DeliveryExecutionLease](tx, executionLeasesBucket)
		return e
	})
	return rows, err
}

func (s *Service) DeliverySaveExecutionBinding(value DeliveryExecutionBinding) error {
	if value.EndpointID < 1 || value.StackID < 1 || value.TaskID == "" || value.DeviceID == "" || value.Grant.ControlEpoch != s.deliveryEpoch {
		return fail(409, "invalid_execution_binding")
	}
	if err := value.Grant.Validate(value.Grant.ExpectedStackVersion, value.Grant.Action, s.opts.Now()); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		task, err := read[Task](tx, "tasks", value.TaskID)
		if err != nil || task.DeviceID != value.DeviceID || task.Target.EndpointID != value.EndpointID {
			return fail(409, "execution_target_changed")
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(executionBindingsBucket)); err != nil {
			return err
		}
		if op, e := read[Operation](tx, "operations", value.Grant.OperationID); e == nil && op.Purpose == "model-validation" && strings.HasPrefix(op.PackageID, "delivery-") {
			if job, e := read[deliveryJobRecord](tx, "delivery-jobs", strings.TrimPrefix(op.PackageID, "delivery-")); e == nil && op.TaskID == job.ValidationTaskID && op.DeviceID == job.ValidationDeviceID {
				value.JobID, value.JobClaimEpoch = job.ID, job.ClaimEpoch
			}
		}
		key := fmt.Sprintf("%d/%d", value.EndpointID, value.StackID)
		old, err := read[DeliveryExecutionBinding](tx, executionBindingsBucket, key)
		if err == nil && (old.TaskID != value.TaskID || old.DeviceID != value.DeviceID || old.Grant.FencingToken > value.Grant.FencingToken) {
			return fail(409, "execution_binding_changed")
		}
		return put(tx, executionBindingsBucket, key, value)
	})
}

func (s *Service) DeliveryManagedStack(stackID int) (bool, error) {
	rows, err := s.DeliveryExecutionBindings()
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.StackID == stackID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) DeliveryExecutionBindings() ([]DeliveryExecutionBinding, error) {
	var rows []DeliveryExecutionBinding
	err := s.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte(executionBindingsBucket)) == nil {
			return nil
		}
		var e error
		rows, e = list[DeliveryExecutionBinding](tx, executionBindingsBucket)
		return e
	})
	return rows, err
}

func (s *Service) DeliveryExecutionOperationAllowed(value DeliveryExecutionBinding) error {
	return s.db.View(func(tx *bolt.Tx) error {
		task, err := read[Task](tx, "tasks", value.TaskID)
		if err != nil || task.Archived || task.DeviceID != value.DeviceID || task.Target.EndpointID != value.EndpointID || (task.Target.StackID != value.StackID && task.State != "provisioning") {
			return fail(409, "execution_target_changed")
		}
		op, err := read[Operation](tx, "operations", value.Grant.OperationID)
		if err == nil {
			if op.TaskID != task.ID || op.DeviceID != task.DeviceID || (op.Status != "dispatching" && op.Status != "awaiting_observation" && op.Status != "succeeded") {
				return fail(409, "execution_operation_inactive")
			}
			if err := s.executionActorAllowed(tx, op.ActorUserID, task, op.PackageID, op.Purpose == "delivery-rollback", value.JobID != ""); err != nil {
				return err
			}
			if value.JobID != "" {
				if op.Purpose != "model-validation" {
					return fail(409, "execution_delivery_validation_changed")
				}
				job, e := read[deliveryJobRecord](tx, "delivery-jobs", value.JobID)
				if e != nil || job.ClaimEpoch != value.JobClaimEpoch || job.ControlEpoch != s.deliveryEpoch || job.ValidationTaskID != task.ID || job.ValidationDeviceID != task.DeviceID || op.PackageID != "delivery-"+job.ID || (job.ValidationOperationID != "" && job.ValidationOperationID != op.ID) {
					return fail(409, "execution_delivery_claim_changed")
				}
				if job.Status == "claimed" {
					if !s.opts.Now().Before(job.LeaseExpiresAt) || job.SecretHash == "" {
						return fail(409, "execution_delivery_claim_expired")
					}
				} else if job.Status != "published" && job.Status != "artifact_ready" {
					return fail(409, "execution_delivery_job_inactive")
				}
				return s.deliveryJobAuthorized(tx, nil, job)
			}
			return nil
		}
		rows, err := list[DeliveryRollout](tx, "delivery-rollouts")
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Status != "dispatching" && row.Status != "running" && row.Status != "rolling_back" {
				continue
			}
			for index, entry := range row.Targets {
				if entry.TaskID == task.ID && (entry.OperationID == value.Grant.OperationID || "rollback-"+entry.OperationID == value.Grant.OperationID) {
					rollback := "rollback-"+entry.OperationID == value.Grant.OperationID
					if rollback && (!row.RollingBack || row.CurrentTarget != index || !contains([]string{"failed", "needs_review", "succeeded", "awaiting_observation", "dispatch_unconfirmed", "dispatching"}, entry.Status)) {
						return fail(409, "execution_operation_inactive")
					}
					if !rollback && entry.Status != "dispatching" && entry.Status != "awaiting_observation" && entry.Status != "succeeded" {
						return fail(409, "execution_operation_inactive")
					}
					return s.executionActorAllowed(tx, row.ActorUserID, task, row.PackageID, rollback, false)
				}
			}
		}
		return fail(409, "execution_operation_inactive")
	})
}

func (s *Service) executionActorAllowed(tx *bolt.Tx, actor int, task Task, packageID string, rollback, validation bool) error {
	device, err := read[Device](tx, "devices", task.DeviceID)
	if err != nil || device.Archived || device.EndpointID != task.Target.EndpointID {
		return fail(409, "execution_target_changed")
	}
	if actor < 1 || s.opts.InspectAccess == nil {
		return fail(503, "execution_authorization_unavailable")
	}
	p, native, err := s.opts.InspectAccess(actor, device.EndpointID)
	if err != nil || !native || p.UserID != actor || !allows(p, device.ACL) || !allows(p, task.ACL) {
		return fail(403, "execution_authorization_changed")
	}
	model, err := read[ModelPackage](tx, "models", packageID)
	if err != nil {
		return fail(409, "execution_model_changed")
	}
	if validation {
		return nil
	}
	if !model.Published || !model.Immutable {
		return fail(403, "execution_publication_changed")
	}
	if !rollback && !s.policyAllows(tx, task, device, model, p) {
		return fail(403, "execution_publication_changed")
	}
	if rollback && !s.deliveryArtifactAllows(tx, task, device, model, p) {
		return fail(403, "execution_publication_changed")
	}
	return nil
}

func (s *Service) DeliveryExecutionOperationBusy(id string) (bool, error) {
	var busy bool
	err := s.db.View(func(tx *bolt.Tx) error {
		op, e := read[Operation](tx, "operations", id)
		if e == nil {
			busy = op.Status == "dispatching" || op.Status == "awaiting_observation"
			return nil
		}
		rows, e := list[DeliveryRollout](tx, "delivery-rollouts")
		if e != nil {
			return e
		}
		for _, row := range rows {
			for _, target := range row.Targets {
				if target.OperationID == id && (target.Status == "dispatching" || target.Status == "awaiting_observation") {
					busy = true
				}
			}
		}
		return nil
	})
	return busy, err
}

func (s *Service) DeliveryExecutionGroupTasks(deviceIDs []string) ([]Task, error) {
	var result []Task
	err := s.db.View(func(tx *bolt.Tx) error {
		rows, e := list[Task](tx, "tasks")
		if e != nil {
			return e
		}
		for _, task := range rows {
			if !task.Archived && contains(deviceIDs, task.DeviceID) {
				result = append(result, task)
			}
		}
		return nil
	})
	return result, err
}

func (s *Service) restoreDeliveryExecution(tx *bolt.Tx) error {
	if tx.Bucket([]byte("delivery-execution-stop-previews")) != nil {
		if err := tx.DeleteBucket([]byte("delivery-execution-stop-previews")); err != nil {
			return err
		}
	}
	for _, kind := range []string{executionBindingsBucket, executionLeasesBucket} {
		if tx.Bucket([]byte(kind)) == nil {
			continue
		}
		cursor := tx.Bucket([]byte(kind)).Cursor()
		for key, value := cursor.First(); key != nil; key, value = cursor.Next() {
			if kind == executionBindingsBucket {
				var row DeliveryExecutionBinding
				if err := json.Unmarshal(value, &row); err != nil {
					return err
				}
				row.Grant.ControlEpoch = ""
				row.Grant.ExpiresAt = time.Time{}
				if err := put(tx, kind, string(key), row); err != nil {
					return err
				}
			} else {
				var row DeliveryExecutionLease
				if err := json.Unmarshal(value, &row); err != nil {
					return err
				}
				row.ControlEpoch = ""
				row.ExpiresAt = time.Time{}
				if err := put(tx, kind, string(key), row); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type DeliveryExecutionStopTarget struct {
	TaskID  string `json:"taskId"`
	StackID int    `json:"stackId"`
	Version int    `json:"version"`
}
type DeliveryExecutionStopPlan struct {
	Recovery      bool                          `json:"recovery"`
	PreviousJobID string                        `json:"previousJobId,omitempty"`
	ID            string                        `json:"id"`
	ActorUserID   int                           `json:"actorUserId"`
	ControlEpoch  string                        `json:"controlEpoch"`
	ResourceGroup string                        `json:"resourceGroup"`
	LeaseID       string                        `json:"leaseId"`
	Targets       []DeliveryExecutionStopTarget `json:"targets"`
	ExpiresAt     time.Time                     `json:"expiresAt"`
	Status        string                        `json:"status"`
	Stopped       bool                          `json:"stopped"`
}

func (s *Service) DeliveryExecutionTask(id string) (Task, error) {
	var task Task
	err := s.db.View(func(tx *bolt.Tx) error { var e error; task, e = read[Task](tx, "tasks", id); return e })
	return task, err
}
func (s *Service) DeliveryExecutionAdmin(r *http.Request) (int, error) {
	p, err := s.principal(r)
	if err != nil {
		return 0, err
	}
	if !p.Admin {
		return 0, fail(403, "administrator_required")
	}
	return p.UserID, nil
}
func (s *Service) DeliverySaveExecutionStopPlan(plan DeliveryExecutionStopPlan) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte("delivery-execution-stop-previews")); err != nil {
			return err
		}
		return put(tx, "delivery-execution-stop-previews", plan.ID, plan)
	})
}
func (s *Service) DeliveryExecutionStopPlan(id string) (DeliveryExecutionStopPlan, error) {
	var plan DeliveryExecutionStopPlan
	err := s.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("delivery-execution-stop-previews")) == nil {
			return errNotFound
		}
		var e error
		plan, e = read[DeliveryExecutionStopPlan](tx, "delivery-execution-stop-previews", id)
		return e
	})
	return plan, err
}

func (s *Service) DeliveryValidateExecutionStopClaim(jobID string, claim DeliveryClaim) (DeliveryJob, error) {
	var job deliveryJobRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		job, e = s.claimDelivery(tx, jobID, claim)
		if e != nil {
			return e
		}
		if e = s.deliveryJobAuthorized(tx, nil, job); e != nil {
			return e
		}
		return nil
	})
	return job.DeliveryJob, err
}
func (s *Service) DeliveryExecutionTaskBusy(taskID string) (bool, error) {
	busy := false
	err := s.db.View(func(tx *bolt.Tx) error {
		rows, e := list[Operation](tx, "operations")
		if e != nil {
			return e
		}
		for _, row := range rows {
			if row.TaskID == taskID && (row.Status == "dispatching" || row.Status == "awaiting_observation") {
				busy = true
			}
		}
		return nil
	})
	return busy, err
}

func (s *Service) DeliveryValidationStopPolicyAllowed(job DeliveryJob) bool {
	allowed := false
	_ = s.db.View(func(tx *bolt.Tx) error {
		policy, e := read[DeliveryPolicy](tx, "delivery-policies", job.PolicyID)
		allowed = e == nil && policy.Enabled && job.Policy.AllowStopValidationTask && policy.AllowStopValidationTask
		return nil
	})
	return allowed
}
