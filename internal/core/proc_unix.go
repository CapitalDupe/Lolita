package core

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// prepareProcess sets up Unix-specific process attributes: a new process
// group so children survive terminal/GUI closure and can be signalled as
// a group.
func prepareProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// interruptProcess sends SIGINT (graceful: simplexmq servers save
// undelivered messages on SIGINT, not SIGTERM).
func interruptProcess(p *os.Process) {
	if p == nil {
		return
	}
	// Signal the process group (negative pid).
	_ = syscall.Kill(-p.Pid, syscall.SIGINT)
	_ = p.Signal(syscall.SIGINT)
}

// pidAlive reports whether a pid belongs to a live process.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// interruptPid sends SIGINT to an adopted process group.
func interruptPid(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGINT)
	_ = syscall.Kill(pid, syscall.SIGINT)
}

// killPid force-kills an adopted process group.
func killPid(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// aliveDetail is extra context for diagnostics (uname of the process).
func aliveDetail(pid int) string {
	return fmt.Sprintf("pid %d", pid)
}
