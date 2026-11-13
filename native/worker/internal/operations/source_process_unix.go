//go:build !windows

package operations

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func runSourceProcess(ctx context.Context, command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	var err error
	canceled := false
	select {
	case err = <-done:
	case <-ctx.Done():
		canceled = true
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	}
	// Drain descendants before the project releases its write fence.
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(-command.Process.Pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		} else {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if canceled {
		<-done
		return ctx.Err()
	}
	if err != nil {
		return errors.New("original source collection failed; inspect the original task's source step")
	}
	return nil
}
