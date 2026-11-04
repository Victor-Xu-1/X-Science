package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestSessionRunnerCommandSkipsLaunchWhenAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("runner cancelled by its operation owner")
	cancel(cause)

	// The native start would return a missing executable error if reached.
	options := SessionRunnerCommandOptions{
		Command:          filepath.Join(t.TempDir(), "missing-runner"),
		OutputLimitBytes: 1024,
	}
	_, err := runSessionRunnerCommand(ctx, options, sessionRunnerCommandInput{})
	if !errors.Is(err, cause) {
		t.Fatalf("already-canceled runner did not preserve its cancellation cause: %v", err)
	}
}
