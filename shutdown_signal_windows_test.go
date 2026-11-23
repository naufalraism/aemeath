//go:build windows

package aemeath_test

import (
	"os/exec"
	"syscall"
	"testing"
)

// A process in its own group can be sent Ctrl+Break without it reaching the
// test process too.
func prepareForSignal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// sendStopSignal presses Ctrl+Break for the server, which Go delivers as
// os.Interrupt: the same path as Ctrl+C in a terminal.
func sendStopSignal(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	const ctrlBreakEvent = 1
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")
	if r, _, err := proc.Call(ctrlBreakEvent, uintptr(cmd.Process.Pid)); r == 0 {
		t.Fatalf("GenerateConsoleCtrlEvent: %v", err)
	}
}
