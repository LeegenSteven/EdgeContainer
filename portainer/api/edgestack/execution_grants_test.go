package edgestack

import (
	"strings"
	"testing"
	"time"

	"github.com/portainer/portainer/api/edge"
	bolt "go.etcd.io/bbolt"
)

func TestExecutionGrantRechecksActorAndPublication(t *testing.T) {
	f, _ := deliveryFixture(t)
	model := ModelPackage{ID: "managed-model", Name: "Managed", Version: "1", Slot: "llm", TaskKinds: []string{"voice"}, Architectures: []string{"amd64"}, Published: true, Immutable: true, Image: "runtime@sha256:" + strings.Repeat("a", 64), ModelHash: "sha256:" + strings.Repeat("b", 64)}
	f.ok(t, "admin", "POST", "/admin/models", model, 200)
	binding := DeliveryExecutionBinding{EndpointID: 1, StackID: 1, DeviceID: "device-a", TaskID: "task-a", Grant: edge.ExecutionGrant{OperationID: "managed-op"}}
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		return put(tx, "operations", "managed-op", Operation{ID: "managed-op", ActorUserID: 2, TaskID: "task-a", DeviceID: "device-a", PackageID: model.ID, Status: "succeeded"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeliveryExecutionOperationAllowed(binding); err != nil {
		t.Fatal(err)
	}
	f.endpointAllowed = false
	if err := f.s.DeliveryExecutionOperationAllowed(binding); err == nil {
		t.Fatal("revoked native ACL still authorized")
	}
	f.endpointAllowed = true
	if err := f.s.db.Update(func(tx *bolt.Tx) error {
		task, e := read[Task](tx, "tasks", "task-a")
		if e != nil {
			return e
		}
		task.ACL = ACL{UserIDs: []int{3}}
		return put(tx, "tasks", task.ID, task)
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeliveryExecutionOperationAllowed(binding); err == nil {
		t.Fatal("revoked task ACL still authorized")
	}
}

func TestValidationExecutionGrantRejectsRetryAndChangedPolicy(t *testing.T) {
	for _, change := range []string{"claim_epoch", "lease_expired", "policy", "request_owner_acl"} {
		t.Run(change, func(t *testing.T) {
			f, token := deliveryFixture(t)
			job, _ := deliveryCreateClaim(t, f, token)
			model := ModelPackage{ID: "delivery-" + job.ID}
			binding := DeliveryExecutionBinding{JobID: job.ID, JobClaimEpoch: job.ClaimEpoch, EndpointID: 1, StackID: 1, DeviceID: "device-a", TaskID: "task-a", Grant: edge.ExecutionGrant{OperationID: "validation-op"}}
			if err := f.s.db.Update(func(tx *bolt.Tx) error {
				if e := put(tx, "models", model.ID, model); e != nil {
					return e
				}
				return put(tx, "operations", "validation-op", Operation{ID: "validation-op", Purpose: "model-validation", ActorUserID: 1, TaskID: "task-a", DeviceID: "device-a", PackageID: model.ID, Status: "succeeded"})
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.s.DeliveryExecutionOperationAllowed(binding); err != nil {
				t.Fatal(err)
			}
			if change == "lease_expired" {
				f.now = f.now.Add(121 * time.Second)
			}
			if err := f.s.db.Update(func(tx *bolt.Tx) error {
				switch change {
				case "claim_epoch":
					row, e := read[deliveryJobRecord](tx, "delivery-jobs", job.ID)
					if e != nil {
						return e
					}
					row.ClaimEpoch++
					return put(tx, "delivery-jobs", row.ID, row)
				case "policy":
					row, e := read[DeliveryPolicy](tx, "delivery-policies", job.PolicyID)
					if e != nil {
						return e
					}
					row.Enabled = false
					return put(tx, "delivery-policies", row.ID, row)
				case "request_owner_acl":
					row, e := read[Device](tx, "devices", "device-a")
					if e != nil {
						return e
					}
					row.ACL = ACL{UserIDs: []int{3}}
					return put(tx, "devices", row.ID, row)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.s.DeliveryExecutionOperationAllowed(binding); err == nil {
				t.Fatal("changed validation authority still authorized")
			}
		})
	}
}

func TestOrdinaryReuseOfDeliveryModelDoesNotCaptureWorkerClaim(t *testing.T) {
	f, token := deliveryFixture(t)
	job, _ := deliveryCreateClaim(t, f, token)
	op := Operation{ID: "normal-deployment", ActorUserID: 2, TaskID: "task-a", DeviceID: "device-a", PackageID: "delivery-" + job.ID, Status: "succeeded"}
	requireNoError := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	requireNoError(f.s.db.Update(func(tx *bolt.Tx) error { return put(tx, "operations", op.ID, op) }))
	binding := DeliveryExecutionBinding{EndpointID: 1, StackID: 1, DeviceID: "device-a", TaskID: "task-a", Grant: edge.ExecutionGrant{OperationID: op.ID, ControlEpoch: f.s.DeliveryControlEpoch(), ResourceGroup: "board", LeaseID: "lease", FencingToken: 1, ExpectedStackVersion: 1, Action: "deploy", ExpiresAt: f.now.Add(time.Hour)}}
	requireNoError(f.s.DeliverySaveExecutionBinding(binding))
	got, known, err := f.s.DeliveryExecutionBinding(1, 1)
	requireNoError(err)
	if !known || got.JobID != "" || got.JobClaimEpoch != 0 {
		t.Fatalf("ordinary deployment captured validation claim: %+v", got)
	}
}
