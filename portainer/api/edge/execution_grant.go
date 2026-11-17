package edge

import (
	"errors"
	"time"
)

// ExecutionGrantsCapability is advertised only by workers that verify a grant
// immediately before, throughout, and after a managed container mutation.
const ExecutionGrantsCapability = "1"
const ExecutionGrantsHeader = "X-EdgeStack-Execution-Grants"

// ExecutionGrant binds a board lease to one control-plane operation and stack
// version. It is an authorization reference, not a credential or a lease grant.
type ExecutionGrant struct {
	ResourceGroup        string    `json:"resourceGroup"`
	LeaseID              string    `json:"leaseId"`
	FencingToken         uint64    `json:"fencingToken"`
	ControlEpoch         string    `json:"controlEpoch"`
	OperationID          string    `json:"operationId"`
	ExpectedStackVersion int       `json:"expectedStackVersion"`
	ExpiresAt            time.Time `json:"expiresAt"`
	Action               string    `json:"action"`
}

// Validate checks the wire shape. The Server must also compare every field to
// its persisted operation and verify the current shared lease authority.
func (g ExecutionGrant) Validate(version int, action string, now time.Time) error {
	if g.ResourceGroup == "" || g.LeaseID == "" || g.FencingToken == 0 || g.ControlEpoch == "" || g.OperationID == "" || version < 1 || g.ExpectedStackVersion != version || g.ExpiresAt.IsZero() || !now.Before(g.ExpiresAt) {
		return errors.New("execution_grant_invalid")
	}
	if action != "pull" && action != "deploy" && action != "remove" {
		return errors.New("execution_action_invalid")
	}
	if g.Action != action && !(g.Action == "deploy" && action == "pull") {
		return errors.New("execution_action_mismatch")
	}
	return nil
}
