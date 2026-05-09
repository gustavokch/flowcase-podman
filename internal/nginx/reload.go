package nginx

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/log"
)

// Reload runs `nginx -s reload` inside the named nginx container.
// Mirrors routes/droplet.py:540-544 — non-zero exit logs WARN with
// the captured stderr so the orchestrator surfaces a syntax error in
// the freshly-written config without crashing.
func Reload(ctx context.Context, dx *dockerx.Client, containerName string) error {
	if containerName == "" {
		return fmt.Errorf("Reload: containerName required")
	}

	created, err := dx.Raw().ContainerExecCreate(ctx, containerName, container.ExecOptions{
		Cmd:          []string{"nginx", "-s", "reload"},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("ExecCreate on %s: %w", containerName, err)
	}

	att, err := dx.Raw().ContainerExecAttach(ctx, created.ID, container.ExecStartOptions{})
	if err != nil {
		return fmt.Errorf("ExecAttach on %s: %w", containerName, err)
	}
	defer att.Close()

	// Drain the multiplexed stream so the daemon marks the exec as
	// finished and ContainerExecInspect returns the real exit code.
	var stdoutBuf, stderrBuf bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdoutBuf, &stderrBuf, att.Reader); err != nil && err != io.EOF {
		return fmt.Errorf("draining exec stream: %w", err)
	}

	insp, err := dx.Raw().ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return fmt.Errorf("ExecInspect: %w", err)
	}
	if insp.ExitCode != 0 {
		// Match the legacy WARN log; the message body is whatever
		// nginx wrote to stderr/stdout.
		out := combine(stdoutBuf.Bytes(), stderrBuf.Bytes())
		log.Warn("Failed to reload Nginx: %s", out)
	}
	return nil
}

// combine prefers stderr (nginx writes errors there) but falls back
// to stdout if stderr was empty.
func combine(stdout, stderr []byte) string {
	if len(stderr) > 0 {
		return string(stderr)
	}
	return string(stdout)
}
