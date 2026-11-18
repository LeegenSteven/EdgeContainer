package edgestack

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func TestExecutionScopeIdentityIsImmutableAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "business.db")
	s, err := Open(path, Options{ExecutionScope: "blue"})
	require.NoError(t, err)
	require.NoError(t, s.VerifyDeliveryExecutionScope("blue"))
	require.NoError(t, s.Close())
	for _, scope := range []string{"green", ""} {
		other, err := Open(path, Options{ExecutionScope: scope})
		require.Nil(t, other)
		require.ErrorContains(t, err, "execution_scope_identity_changed")
	}
	s, err = Open(path, Options{ExecutionScope: "blue"})
	require.NoError(t, err)
	require.NoError(t, s.Close())
}

func TestOlderExecutionRecordsCannotBeReboundToNewScope(t *testing.T) {
	for _, record := range []string{"binding", "unreleased_lease"} {
		t.Run(record, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "business.db")
			s, err := Open(path, Options{})
			require.NoError(t, err)
			require.NoError(t, s.db.Update(func(tx *bolt.Tx) error {
				if err := tx.DeleteBucket([]byte(executionIdentityBucket)); err != nil {
					return err
				}
				if record == "binding" {
					return put(tx, executionBindingsBucket, "4/1", DeliveryExecutionBinding{EndpointID: 4, StackID: 1})
				}
				return put(tx, executionLeasesBucket, "board", DeliveryExecutionLease{ResourceGroup: "board", LeaseID: "old", Released: false})
			}))
			require.NoError(t, s.Close())
			require.NoError(t, os.Remove(filepath.Join(filepath.Dir(path), executionScopeMarker)))
			rebound, err := Open(path, Options{ExecutionScope: "blue"})
			require.Nil(t, rebound)
			require.ErrorContains(t, err, "legacy_execution_scope_migration_refused")
			s, err = Open(path, Options{})
			require.NoError(t, err)
			require.NoError(t, s.Close())
		})
	}
}

func TestExecutionScopeLiveRestoreCannotBypassIdentity(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "business.db"), Options{ExecutionScope: "blue"})
	require.NoError(t, err)
	defer s.Close()
	require.NoError(t, s.db.Update(func(tx *bolt.Tx) error {
		return put(tx, executionIdentityBucket, "scope", executionScopeIdentity{Version: 1, Scope: "green"})
	}))
	require.ErrorContains(t, s.VerifyDeliveryExecutionScope("blue"), "execution_scope_identity_changed")
	require.Error(t, s.ApplyRestorePolicy(nil, nil))
	require.Error(t, s.BackupTo(io.Discard))
}

func TestExecutionScopeMarkerSurvivesBackupPredatingDBIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "business.db")
	s, err := Open(path, Options{ExecutionScope: "blue"})
	require.NoError(t, err)
	require.NoError(t, s.db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte(executionIdentityBucket)) }))
	require.Error(t, s.VerifyDeliveryExecutionScope("blue"), "live DB identity loss must not auto-rebind")
	require.NoError(t, s.Close())
	other, err := Open(path, Options{ExecutionScope: "green"})
	require.Nil(t, other)
	require.ErrorContains(t, err, "execution_scope_identity_changed")
	s, err = Open(path, Options{ExecutionScope: "blue"})
	require.NoError(t, err)
	require.NoError(t, s.Close())
}
