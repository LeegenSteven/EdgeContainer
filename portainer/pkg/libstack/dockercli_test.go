package libstack

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/docker/cli/cli/command"
	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/stretchr/testify/require"
)

func isolatedDockerConfig(t *testing.T) {
	t.Helper()
	directory := t.TempDir()
	previous := dockerconfig.Dir()
	t.Setenv("DOCKER_CONFIG", directory)
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	dockerconfig.SetDir(directory)
	t.Cleanup(func() { dockerconfig.SetDir(previous) })
}

func TestWithCli_ExecutionContextPreservesCancellation(t *testing.T) {
	isolatedDockerConfig(t)
	type valueKey struct{}
	parent, cancelDeadline := context.WithTimeout(context.WithValue(t.Context(), valueKey{}, "managed"), time.Minute)
	defer cancelDeadline()
	parent, cancel := context.WithCancelCause(parent)
	cause := errors.New("execution lease revoked")
	// Child wrappers must retain the opt-in along the Compose call chain.
	marked, cancelChild := context.WithCancel(WithExecutionContext(parent))
	defer cancelChild()

	err := WithCli(marked, DockerCliOptions{Host: "tcp://127.0.0.1:1"}, func(ctx context.Context, _ *command.DockerCli) error {
		require.Equal(t, "managed", ctx.Value(valueKey{}))
		wantedDeadline, _ := parent.Deadline()
		actualDeadline, hasDeadline := ctx.Deadline()
		require.True(t, hasDeadline)
		require.Equal(t, wantedDeadline, actualDeadline)
		cancel(cause)
		select {
		case <-ctx.Done():
			require.ErrorIs(t, context.Cause(ctx), cause)
			return ctx.Err()
		case <-time.After(time.Second):
			t.Fatal("WithCli discarded the managed execution cancellation")
			return nil
		}
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestWithCli_UnmarkedContextKeepsLegacyBackground(t *testing.T) {
	isolatedDockerConfig(t)
	type valueKey struct{}
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), valueKey{}, "legacy"), time.Minute)
	cancel()
	err := WithCli(ctx, DockerCliOptions{Host: "tcp://127.0.0.1:1"}, func(actual context.Context, _ *command.DockerCli) error {
		require.Nil(t, actual.Done())
		require.NoError(t, actual.Err())
		require.Nil(t, actual.Value(valueKey{}))
		_, hasDeadline := actual.Deadline()
		require.False(t, hasDeadline)
		return nil
	})
	require.NoError(t, err)
}

func TestWithCli_InjectsHeaders(t *testing.T) {
	const headerName = "X-PortainerAgent-ManagerOperation"

	err := WithCli(
		t.Context(),
		DockerCliOptions{
			Host:    "tcp://127.0.0.1:1",
			Headers: map[string]string{headerName: "1"},
		},
		func(_ context.Context, cli *command.DockerCli) error {
			require.Equal(t, "1", cli.ConfigFile().HTTPHeaders[headerName])
			return nil
		},
	)
	require.NoError(t, err)
}

func TestWithCli_NoHeaders(t *testing.T) {
	err := WithCli(
		context.Background(),
		DockerCliOptions{Host: "tcp://127.0.0.1:1"},
		func(_ context.Context, cli *command.DockerCli) error {
			require.Empty(t, cli.ConfigFile().HTTPHeaders)
			return nil
		},
	)
	require.NoError(t, err)
}
