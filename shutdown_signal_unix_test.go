//go:build !windows

package aemeath_test

import (
	"os/exec"
	"syscall"
	"testing"
)

func prepareForSignal(*exec.Cmd) {}

// sendStopSignal stops the server the way Docker and Kubernetes do.
func sendStopSignal(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
}
