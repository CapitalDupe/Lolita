package core

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProcState is the lifecycle state of a supervised process.
type ProcState string

const (
	ProcStopped  ProcState = "stopped"
	ProcStarting ProcState = "starting"
	ProcRunning  ProcState = "running"
	ProcStopping ProcState = "stopping"
	ProcFailed   ProcState = "failed"
)

// ProcStatus describes a supervised process at a moment in time.
type ProcStatus struct {
	Kind     Kind
	State    ProcState
	PID      int
	Started  time.Time
	ExitInfo string
	Adopted  bool // running under a previous sxops instance
}

// Proc is a supervised child process.
//
// Children are fully detachable: they log straight into their log file
// (inherited fd, no pipe to this process) and run in their own process
// group, so they survive the GUI or daemon exiting. Pidfiles allow a
// later sxops instance to adopt (observe / stop) them.
type Proc struct {
	mu       sync.Mutex
	kind     Kind
	cmd      *exec.Cmd
	state    ProcState
	started  time.Time
	exitInfo string

	stopCh    chan struct{}
	stoppedBy bool // user-requested stop
	onExit    func(ProcStatus)

	autoRestart bool
	restarts    int
}

// Supervisor manages all child processes.
type Supervisor struct {
	mu    sync.Mutex
	procs map[Kind]*Proc
	paths *Paths
	state *StateStore
	// PreStart, when set, runs right before a process is spawned and
	// can veto the start. The engine uses it to bring up the private
	// PostgreSQL instance before the NTF server - covering every start
	// path at once: the dashboard Start button, Start all, the
	// autostart daemon, crash auto-restart and updates.
	PreStart func(Kind) error
}

// NewSupervisor creates a supervisor.
func NewSupervisor(paths *Paths, state *StateStore) *Supervisor {
	return &Supervisor{procs: map[Kind]*Proc{}, paths: paths, state: state}
}

// Statuses snapshots all processes (in-process + adopted via pidfiles).
func (sv *Supervisor) Statuses() map[Kind]ProcStatus {
	out := map[Kind]ProcStatus{}
	sv.mu.Lock()
	for k, p := range sv.procs {
		out[k] = p.statusLocked()
	}
	sv.mu.Unlock()
	for _, k := range []Kind{KindSMP, KindXFTP, KindNTF, KindRelay} {
		if _, ok := out[k]; ok {
			continue
		}
		if st := sv.adoptedStatus(k); st != nil {
			out[k] = *st
		}
	}
	return out
}

// Status returns the status of one kind (zero value when unknown).
func (sv *Supervisor) Status(k Kind) ProcStatus {
	sv.mu.Lock()
	p, ok := sv.procs[k]
	sv.mu.Unlock()
	if ok {
		return p.statusLocked()
	}
	if st := sv.adoptedStatus(k); st != nil {
		return *st
	}
	return ProcStatus{Kind: k, State: ProcStopped}
}

// adoptedStatus checks the pidfile for a kind started by a previous
// sxops instance that is still alive.
func (sv *Supervisor) adoptedStatus(k Kind) *ProcStatus {
	pid, ok := readPidFile(sv.paths.PidFile(k))
	if !ok || !pidAlive(pid) {
		return nil
	}
	return &ProcStatus{Kind: k, State: ProcRunning, PID: pid, Adopted: true}
}

func (p *Proc) statusLocked() ProcStatus {
	st := ProcStatus{Kind: p.kind, State: p.state, Started: p.started, ExitInfo: p.exitInfo}
	if p.cmd != nil && p.cmd.Process != nil && p.state == ProcRunning {
		st.PID = p.cmd.Process.Pid
	}
	return st
}

// Start launches a managed server with default arguments.
func (sv *Supervisor) Start(k Kind) error {
	args := StartArgs(k)
	if k == KindRelay {
		return fmt.Errorf("relay requires explicit arguments - use StartWithArgs")
	}
	return sv.StartWithArgs(k, args, nil)
}

// StartWithArgs launches a process with explicit argv and extra env.
// It is idempotent: when the kind already runs (in-process or adopted
// via pidfile) it returns nil without spawning a duplicate.
func (sv *Supervisor) StartWithArgs(k Kind, args []string, extraEnv []string) error {
	sv.mu.Lock()
	if p, ok := sv.procs[k]; ok && (p.state == ProcRunning || p.state == ProcStarting) {
		sv.mu.Unlock()
		return nil // already running in-process (idempotent)
	}
	sv.mu.Unlock()

	// Adopted instance from a previous sxops run?
	if pid, ok := readPidFile(sv.paths.PidFile(k)); ok && pidAlive(pid) {
		return nil // already running (adopted)
	}

	rec := sv.state.Server(k)
	if k != KindRelay && !rec.Installed {
		return fmt.Errorf("%s is not installed", Def(k).Name)
	}
	bin := sv.paths.BinPath(k)
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("binary not found: %s", bin)
	}
	if sv.PreStart != nil {
		if err := sv.PreStart(k); err != nil {
			return err
		}
	}

	// Fresh log per run (O_TRUNC): the relay-address watcher relies on
	// the file reflecting only the current run.
	logf, err := os.OpenFile(sv.paths.ProcLogFile(k), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	env := os.Environ()
	if len(extraEnv) > 0 {
		env = append(env, extraEnv...)
	}
	if k != KindRelay {
		d := Def(k)
		env = append(env,
			d.CfgEnvVar+"="+sv.paths.CfgDir(k),
			d.LogEnvVar+"="+sv.paths.LogDir(k),
		)
	}

	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdout = logf // inherited fd: child survives sxops exit
	cmd.Stderr = logf
	prepareProcess(cmd)

	p := &Proc{
		kind:        k,
		cmd:         cmd,
		state:       ProcStarting,
		stopCh:      make(chan struct{}),
		autoRestart: sv.state.Settings().AutoRestart,
	}
	if err := cmd.Start(); err != nil {
		logf.Close()
		p.state = ProcFailed
		p.exitInfo = err.Error()
		return err
	}
	logf.Close() // the child holds its own duplicate of the fd
	pid := cmd.Process.Pid
	writePidFile(sv.paths.PidFile(k), pid)

	sv.mu.Lock()
	sv.procs[k] = p
	sv.mu.Unlock()
	p.state = ProcRunning
	p.started = time.Now()

	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		wasUserStop := p.stoppedBy
		p.state = ProcStopped
		if err != nil && !wasUserStop {
			p.state = ProcFailed
			p.exitInfo = err.Error()
		} else if err != nil && wasUserStop {
			p.exitInfo = ""
		}
		close(p.stopCh)
		p.mu.Unlock()
		// Remove our pidfile only if it still points to this process.
		removePidFileIf(sv.paths.PidFile(k), pid)

		sv.mu.Lock()
		p2 := sv.procs[k]
		sv.mu.Unlock()
		if p2 == p && p.onExit != nil {
			p.onExit(p.statusLocked())
		}
		// Auto-restart on unexpected exit with backoff.
		if !wasUserStop && p.autoRestart && p.restarts < 5 {
			p.restarts++
			time.Sleep(3 * time.Second)
			if st := sv.Status(k); st.State == ProcStopped || st.State == ProcFailed {
				_ = sv.Start(k)
			}
		}
	}()
	return nil
}

// Stop terminates a process - ours or an adopted one. On Unix the server
// receives SIGINT so it saves undelivered messages before exiting.
func (sv *Supervisor) Stop(k Kind, grace time.Duration) error {
	sv.mu.Lock()
	p, ok := sv.procs[k]
	sv.mu.Unlock()
	if !ok || p == nil {
		return sv.stopAdopted(k, grace)
	}
	p.mu.Lock()
	if p.state != ProcRunning && p.state != ProcStarting {
		p.mu.Unlock()
		return nil
	}
	p.stoppedBy = true
	p.state = ProcStopping
	proc := p.cmd.Process
	pid := proc.Pid
	p.mu.Unlock()

	interruptProcess(proc)

	done := make(chan struct{})
	go func() {
		<-p.stopCh
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(grace):
		_ = proc.Kill()
		<-done
	}
	removePidFileIf(sv.paths.PidFile(k), pid)
	return nil
}

// stopAdopted stops a process started by a previous sxops instance.
func (sv *Supervisor) stopAdopted(k Kind, grace time.Duration) error {
	pid, ok := readPidFile(sv.paths.PidFile(k))
	if !ok {
		return nil
	}
	if !pidAlive(pid) {
		removePidFileIf(sv.paths.PidFile(k), pid)
		return nil
	}
	interruptPid(pid)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			removePidFileIf(sv.paths.PidFile(k), pid)
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	killPid(pid)
	removePidFileIf(sv.paths.PidFile(k), pid)
	return nil
}

// StopAll stops every supervised or adopted process.
func (sv *Supervisor) StopAll(grace time.Duration) {
	for k := range sv.Statuses() {
		_ = sv.Stop(k, grace)
	}
}

// WaitRunning blocks until kind is running or timeout.
func (sv *Supervisor) WaitRunning(k Kind, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st := sv.Status(k); st.State == ProcRunning {
			// Give listeners a moment to bind.
			time.Sleep(700 * time.Millisecond)
			if st2 := sv.Status(k); st2.State == ProcRunning {
				return nil
			}
		} else if st := sv.Status(k); st.State == ProcFailed {
			return fmt.Errorf("%s failed to start: %s", Def(k).Name, st.ExitInfo)
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("%s did not start within %s", Def(k).Name, timeout)
}

// StartEnabled starts all enabled+installed servers plus the relay.
func (sv *Supervisor) StartEnabled() error {
	s := sv.state.Get()
	for _, k := range []Kind{KindSMP, KindXFTP, KindNTF, KindRelay} {
		rec := s.Servers[k]
		if rec == nil || !rec.Enabled || !rec.Installed {
			continue
		}
		if err := sv.Start(k); err != nil {
			return fmt.Errorf("%s: %v", Def(k).Name, err)
		}
	}
	return nil
}

// CommandOutput runs a one-off child command with the portable env and
// captures combined output (used for `init`, `cert`, relay `-e` commands).
func CommandOutput(bin string, args []string, env []string, timeout time.Duration) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if timeout > 0 {
		cmd.WaitDelay = timeout
	}
	err := cmd.Run()
	return out.String(), err
}

// ---- pidfiles ----

func readPidFile(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func writePidFile(path string, pid int) {
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600)
}

// removePidFileIf removes the pidfile only when it still records pid.
func removePidFileIf(path string, pid int) {
	if cur, ok := readPidFile(path); ok && cur == pid {
		_ = os.Remove(path)
	}
}

// tailFile returns the last n lines of a file.
func tailFile(path string, n int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	ls := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n"), nil
}
