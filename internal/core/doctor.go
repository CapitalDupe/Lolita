package core

import (
        "fmt"
        "net"
        "os/exec"
        "strconv"
)

// Check is one diagnostics result.
type Check struct {
        Name    string
        OK      bool
        Detail  string
        FixHint string
}

// Doctor runs environment diagnostics for the portable profile.
func Doctor(paths *Paths) []Check {
        var checks []Check

        // openssl
        if _, err := exec.LookPath("openssl"); err == nil {
                checks = append(checks, Check{Name: "openssl", OK: true, Detail: "found in PATH (used by server init for certificates)"})
        } else {
                checks = append(checks, Check{Name: "openssl", OK: false, Detail: "not found in PATH",
                        FixHint: "Install OpenSSL (e.g. 'sudo apt install openssl') - required to initialize servers"})
        }

        // tor
        tm := NewTorManager(paths, NewMemState(paths), nil)
        if bin, err := tm.FindTor(); err == nil {
                checks = append(checks, Check{Name: "Tor", OK: true, Detail: bin})
        } else {
                checks = append(checks, Check{Name: "Tor", OK: false, Detail: "not found",
                        FixHint: "Install tor (sudo apt install tor) - needed for onion addresses without port forwarding"})
        }

        // ports: a "busy" port is only a problem when somebody ELSE holds it.
        // Our own supervised servers (this run or adopted via pidfile) are
        // exactly what should be listening there. The check uses the port
        // from the saved settings when the server was already initialized.
        st := NewMemState(paths)
        for _, k := range []Kind{KindSMP, KindXFTP} {
                p := DefaultPort(k)
                if rec := st.Server(k); rec != nil && rec.Port != 0 {
                        p = rec.Port
                }
                detail := "free"
                ok := true
                fix := ""
                if l, err := net.Listen("tcp", ":"+strconv.Itoa(p)); err == nil {
                        l.Close()
                } else if supervisorHoldsPort(paths, k) {
                        ok = true
                        detail = "in use by your own " + Def(k).Name + " (started by sxops)"
                } else {
                        ok = false
                        detail = "in use"
                        fix = fmt.Sprintf("Another process listens on %d (another SimpleX server?). sxops will automatically move the server to a free local port when it starts - the advertised address is not affected.", p)
                }
                checks = append(checks, Check{Name: fmt.Sprintf("port %d (%s)", p, k), OK: ok, Detail: detail, FixHint: fix})
        }

        return checks
}

// supervisorHoldsPort reports whether the pidfile of kind points at a
// live process - i.e. the port is held by the sxops-managed server
// itself (started by this or a previous sxops run), not a stranger.
func supervisorHoldsPort(paths *Paths, k Kind) bool {
        pid, ok := readPidFile(paths.PidFile(k))
        return ok && pidAlive(pid)
}

// NewMemState creates an in-memory state store (used by standalone
// diagnostics before a real store exists).
func NewMemState(paths *Paths) *StateStore {
        st, err := NewStateStore(paths)
        if err != nil {
                // should not happen; fall back to isolated empty store
                s := &State{Version: stateVersion, Settings: defaultSettings(), Servers: map[Kind]*ServerRec{}}
                return &StateStore{state: s}
        }
        return st
}
