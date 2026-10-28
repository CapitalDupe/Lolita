package core

import (
        "context"
        "fmt"
        "os"
        "path/filepath"
        "strconv"
        "strings"
        "time"
)

// This file implements automatic port conflict resolution for the managed
// servers ("if a port is taken, find a free port and use it").
//
// Design: the ADVERTISED port in the client-facing address (and the onion
// service virtual port) stays at the canonical value - 5223 for SMP, 5225
// for XFTP, 5227 for NTF - so the address a user already pasted into the
// SimpleX app keeps working forever. When the canonical LOCAL port is
// occupied by another program at server start time (a second smp-server,
// the distro Tor service, anything), sxops silently moves the server to a
// nearby free local port, records it in ServerRec.LocalPort, patches the
// server INI and re-maps the onion service (virtual port -> new local
// port). The control and web info ports are re-picked the same way.

// pickFreePortNear returns a free TCP port close to base, skipping every
// port in exclude. base itself is never returned (it is the port that was
// found occupied). 0 when nothing was free in the window.
func pickFreePortNear(base int, exclude map[int]bool) int {
        for p := base + 1; p <= base+50; p++ {
                if exclude[p] {
                        continue
                }
                if tcpPortFree(p) {
                        return p
                }
        }
        return 0
}

// reservedPorts collects every port sxops already manages (all server
// kinds except k, plus the Tor SOCKS/control ports), so a re-picked port
// never collides with another managed component.
func (e *Engine) reservedPorts(except Kind) map[int]bool {
        out := map[int]bool{}
        s := e.State.Get()
        for _, kk := range []Kind{KindSMP, KindXFTP, KindNTF, KindRelay} {
                if kk == except {
                        continue
                }
                if rec := s.Servers[kk]; rec != nil {
                        for _, p := range []int{rec.Port, rec.LocalPort, rec.ControlPort, rec.WebPort, rec.WebHttpsPort} {
                                if p != 0 {
                                        out[p] = true
                                }
                        }
                }
        }
        if s.Tor.SocksPort != 0 {
                out[s.Tor.SocksPort] = true
        }
        if s.Tor.ControlPort != 0 {
                out[s.Tor.ControlPort] = true
        }
        return out
}

// ownServerHoldsPort reports whether the pidfile of kind points at a
// live process - i.e. the port is held by the sxops-managed server of
// this kind itself (this run or adopted), not a stranger.
func (e *Engine) ownServerHoldsPort(k Kind) bool {
        pid, ok := readPidFile(e.Paths.PidFile(k))
        return ok && pidAlive(pid)
}

// EnsureServerPorts checks that the transport / control / web ports of
// kind k are actually available on 127.0.0.1 and moves the LOCAL listen
// ports of any that are taken by a foreign program. It patches the server
// INI and persists the new ports; the advertised (virtual) ports stay
// unchanged, so client addresses are unaffected. It returns one line per
// move for the log.
//
// It is safe to call repeatedly (idempotent while the ports are free) and
// is a no-op for kinds that are not initialized or already running.
func (e *Engine) EnsureServerPorts(k Kind) ([]string, error) {
        if k == KindRelay {
                return nil, nil // the relay has no listening port
        }
        rec := e.State.Server(k)
        if !rec.Installed {
                return nil, nil
        }
        d := Def(k)
        if d.IniName == "" {
                return nil, nil
        }
        if st := e.Sv.Status(k); st.State == ProcRunning || st.State == ProcStarting || e.ownServerHoldsPort(k) {
                return nil, nil // already up: the port holder is our own server
        }

        // Read the current INI (source of truth for what the server binds).
        iniPath := filepath.Join(e.Paths.CfgDir(k), d.IniName)
        if _, err := os.Stat(iniPath); err != nil {
                return nil, nil // not initialized yet - defaults are used at init time
        }
        ini, err := LoadIni(iniPath)
        if err != nil {
                return nil, err
        }

        advertised := rec.Port
        if advertised == 0 {
                advertised = DefaultPort(k)
        }
        local := rec.LocalPort
        if local == 0 {
                local = advertised
        }
        ctrl := rec.ControlPort
        if ctrl == 0 {
                ctrl = DefaultControlPort(k)
        }
        web := rec.WebPort
        if web == 0 {
                web = DefaultWebPort(k)
        }
        // v1.6.29: the SMP server's HTTPS web page port. It is managed
        // whenever info pages are enabled (the torrc maps virtual 443 to
        // it even before the INI carries the key), or when the INI has
        // it active; other kinds never have one.
        webHttps := rec.WebHttpsPort
        if cur, ok := ini.Get("WEB", "https"); ok {
                if p, err := strconv.Atoi(cur); err == nil && p > 0 {
                        webHttps = p
                }
        } else if k == KindSMP && e.State.Settings().EnableInfoPages {
                if webHttps == 0 {
                        webHttps = DefaultWebHttpsPort(k)
                }
        } else {
                webHttps = 0
        }
        // The INI is the source of truth for the ports the server binds
        // (it is what the next start actually reads).
        if cur, ok := ini.Get("TRANSPORT", "port"); ok {
                if p, err := strconv.Atoi(cur); err == nil && p > 0 {
                        local = p
                }
        }
        if cur, ok := ini.Get("TRANSPORT", "control_port"); ok {
                if p, err := strconv.Atoi(cur); err == nil && p > 0 {
                        ctrl = p
                }
        } else {
                ctrl = 0 // no control port configured for this server
        }
        if cur, ok := ini.Get("WEB", "http"); ok {
                if p, err := strconv.Atoi(cur); err == nil && p > 0 {
                        web = p
                }
        } else {
                web = 0 // info page disabled
        }
        moved := []string{}
        exclude := e.reservedPorts(k)
        // The kind's OWN current ports must not be picked either: moving
        // the transport port onto the server's own control port would
        // make it fail to bind at startup (two listeners on one port).
        for _, p := range []int{advertised, local, ctrl, web, webHttps} {
                if p != 0 {
                        exclude[p] = true
                }
        }
        changed := false

        // relocate moves one port when the reason set says it must move
        // (taken by a foreign program, or collided with another port of
        // this very server that just moved).
        relocate := func(name string, cur int, mustMove bool, set func(np int, nps string)) {
                if cur == 0 || (!mustMove && tcpPortFree(cur)) {
                        return
                }
                np := pickFreePortNear(cur, exclude)
                if np == 0 {
                        e.Log.Detail(fmt.Sprintf("warning: the %s port %d is already in use and no free port was found near it - the server may fail to start",
                                name, cur))
                        return
                }
                exclude[np] = true
                set(np, strconv.Itoa(np))
                changed = true
                moved = append(moved, fmt.Sprintf("%s: local port %d was already in use - using %d instead (the server address is unchanged)",
                        d.Name, cur, np))
        }

        // transport port (the one clients reach through the onion service)
        newLocal := local
        relocate("transport", local, false, func(np int, nps string) {
                ini.Set("TRANSPORT", "port", nps)
                newLocal = np
                _ = e.State.UpdateServer(k, func(r *ServerRec) { r.LocalPort = np })
        })
        // control port (admin / stats API) - moves when taken OR when the
        // transport just moved onto it (two listeners cannot share a port)
        if ctrl != 0 {
                relocate("control", ctrl, ctrl == newLocal, func(np int, nps string) {
                        ini.Set("TRANSPORT", "control_port", nps)
                        _ = e.State.UpdateServer(k, func(r *ServerRec) { r.ControlPort = np })
                })
        }
        // web info page port - same collision logic
        if web != 0 {
                relocate("web", web, web == newLocal, func(np int, nps string) {
                        ini.Set("WEB", "http", nps)
                        _ = e.State.UpdateServer(k, func(r *ServerRec) { r.WebPort = np })
                })
        }
        // SMP HTTPS web page port (virtual 443 target) - same collision logic
        if webHttps != 0 {
                relocate("web https", webHttps, webHttps == newLocal, func(np int, nps string) {
                        ini.Set("WEB", "https", nps)
                        _ = e.State.UpdateServer(k, func(r *ServerRec) { r.WebHttpsPort = np })
                })
        }

        if !changed {
                return moved, nil
        }
        if err := ini.Save(); err != nil {
                return moved, fmt.Errorf("%s: saving the new port in %s: %v", d.Name, d.IniName, err)
        }
        return moved, nil
}

// ensurePortsAndStart is the native start path: resolve port conflicts,
// log any moves, start the server and - when a port actually moved while
// Tor is running - regenerate the torrc and bounce Tor so the onion
// service maps the virtual port to the new local port.
func (e *Engine) ensurePortsAndStart(k Kind) error {
        moved, err := e.EnsureServerPorts(k)
        if err != nil {
                return fmt.Errorf("%s: %v", Def(k).Name, err)
        }
        for _, m := range moved {
                e.Log.Step(m)
        }
        if err := e.Sv.Start(k); err != nil {
                return err
        }
        // A server that cannot bind its port dies a second after launch
        // (e.g. a conflict the free-port scan could not solve). Surfacing it
        // right away - with the server's own log lines - beats a dashboard
        // that flips from "running" to "failed" with no reason anywhere.
        if err := e.waitStartStable(k, 8*time.Second); err != nil {
                return err
        }
        if len(moved) > 0 && e.Tor.Running() {
                // A running Tor keeps its old in-memory port mapping; restart it
                // in the background so the onion service follows the moved port.
                // Hidden-service keys persist, so the onion addresses are stable.
                e.Log.Step("Restarting Tor so the onion service follows the new local port")
                go func() {
                        ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
                        defer cancel()
                        _ = e.Tor.Start(ctx)
                }()
        }
        return nil
}

// waitStartStable waits a few seconds for the freshly started server to
// stay up, returning its log tail when it exited immediately.
func (e *Engine) waitStartStable(k Kind, window time.Duration) error {
        deadline := time.Now().Add(window)
        for time.Now().Before(deadline) {
                st := e.Sv.Status(k)
                if st.State == ProcFailed {
                        tail, _ := e.Backend.ServerLogTail(k, 15)
                        msg := fmt.Sprintf("%s exited during startup: %s", Def(k).Name, st.ExitInfo)
                        if s := strings.TrimSpace(string(tail)); s != "" {
                                msg += "\n" + s
                        }
                        return fmt.Errorf("%s", msg)
                }
                if st.State == ProcRunning && time.Since(st.Started) > 3*time.Second {
                        return nil
                }
                time.Sleep(500 * time.Millisecond)
        }
        return nil
}

// localBindPort returns the port kind k actually binds on 127.0.0.1
// (rec.LocalPort when a conflict moved it, the canonical port otherwise).
func (e *Engine) localBindPort(k Kind) int {
        rec := e.State.Server(k)
        if rec.LocalPort != 0 {
                return rec.LocalPort
        }
        if rec.Port != 0 {
                return rec.Port
        }
        return DefaultPort(k)
}
