package core

import (
        "fmt"
        "net"
        "os"
        "path/filepath"
        "strings"
        "time"
)

// Blocking new links / connections.
//
// The SMP protocol has NO per-client identity: the only persistent entity
// is the queue, so a banned client can always come back by creating new
// contact addresses, group invites or conversations (every NEW is a fresh
// queue with fresh keys and no link to the previous one). The lever the
// official SimpleX servers themselves use during spam waves is disabling
// new registrations server-wide:
//
//      [AUTH] new_queues = off   (smp-server.ini, default on)
//      [AUTH] new_files  = off   (file-server.ini, default on)
//
// (simplexmq Server/Main.hs: allowNewQueues = fromMaybe True $ iniOnOff
// "AUTH" "new_queues" ini - the parser accepts exactly "on"/"off" and
// reads the config only at start, so toggling requires a restart.)
// Existing chats keep working either way - only creation is refused.

// newLinksKey is the [AUTH] ini key gating new entity creation per kind.
func newLinksKey(k Kind) string {
        switch k {
        case KindSMP:
                return "new_queues"
        case KindXFTP:
                return "new_files"
        }
        return ""
}

// NewLinksSupported reports whether a kind accepts new-link blocking
// (the SMP and XFTP servers do; the notification server does not).
func NewLinksSupported(k Kind) bool { return newLinksKey(k) != "" }

// NewLinksBlocked reports whether the server refuses to create new
// queues (SMP: contact addresses, group invites, new chats) or new files
// (XFTP uploads). The INI is the source of truth - sxops reads it live
// so hand-edits are picked up on the next refresh.
func (e *Engine) NewLinksBlocked(k Kind) (bool, error) {
        key := newLinksKey(k)
        if key == "" {
                return false, fmt.Errorf("%s does not accept new-link blocking", Def(k).Name)
        }
        if !e.State.Server(k).Installed {
                return false, fmt.Errorf("%s is not installed yet", Def(k).Name)
        }
        ini, err := e.loadServerIni(k)
        if err != nil {
                return false, err
        }
        v, ok := ini.Get("AUTH", key)
        // absent or "on" = allowed; only an exact "off" blocks (the upstream
        // iniOnOff parser errors on anything else at server start anyway).
        return ok && strings.TrimSpace(v) == "off", nil
}

// SetNewLinksBlocked turns new queue/file creation off (or back on) in
// the server's INI and restarts the server when it is running, so the
// change takes effect immediately. The chat protocol cannot block a
// single client from creating links - this is always server-wide, the
// same measure the official SimpleX servers use.
func (e *Engine) SetNewLinksBlocked(k Kind, blocked bool) error {
        key := newLinksKey(k)
        if key == "" {
                return fmt.Errorf("%s does not accept new-link blocking", Def(k).Name)
        }
        if !e.State.Server(k).Installed {
                return fmt.Errorf("%s is not installed yet", Def(k).Name)
        }
        ini, err := e.loadServerIni(k)
        if err != nil {
                return err
        }
        value := "on"
        if blocked {
                value = "off"
        }
        ini.Set("AUTH", key, value)
        if err := e.saveServerIni(k, ini); err != nil {
                return err
        }
        if !e.Backend.ServerRunning(k) {
                // not running: the INI applies at the next start
                return nil
        }
        // the servers read their INI once at start - restart to apply now
        if err := e.Backend.StopServer(k); err != nil {
                return fmt.Errorf("stopping %s after the INI change: %v", Def(k).Name, err)
        }
        if err := e.Backend.StartServer(k); err != nil {
                return fmt.Errorf("starting %s after the INI change: %v", Def(k).Name, err)
        }
        // wait briefly for the control port so an immediate UI refresh
        // does not race the server coming up (harmless if it never opens)
        e.waitControlPort(k, 10*time.Second)
        return nil
}

// waitControlPort polls the local control port until it accepts
// connections or the timeout passes.
func (e *Engine) waitControlPort(k Kind, timeout time.Duration) {
        port := e.State.Server(k).ControlPort
        if port == 0 {
                return
        }
        deadline := time.Now().Add(timeout)
        for time.Now().Before(deadline) {
                conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
                if err == nil {
                        conn.Close()
                        return
                }
                time.Sleep(500 * time.Millisecond)
        }
}

// loadServerIni reads a server's INI through the active backend into the
// order-preserving INI document (via a temp file, like patchServerIni).
func (e *Engine) loadServerIni(k Kind) (*IniFile, error) {
        d := Def(k)
        data, err := e.Backend.ReadServerFile(k, d.IniName)
        if err != nil {
                return nil, err
        }
        tmp := filepath.Join(os.TempDir(), fmt.Sprintf("sxops-%s-nl.ini", k))
        if err := os.WriteFile(tmp, data, 0o600); err != nil {
                return nil, err
        }
        return LoadIni(tmp)
}

// saveServerIni writes an INI document back through the active backend.
func (e *Engine) saveServerIni(k Kind, ini *IniFile) error {
        d := Def(k)
        tmp := filepath.Join(os.TempDir(), fmt.Sprintf("sxops-%s-nl.ini", k))
        if err := os.WriteFile(tmp, []byte(ini.Text()), 0o600); err != nil {
                return err
        }
        out, err := os.ReadFile(tmp)
        if err != nil {
                return err
        }
        os.Remove(tmp)
        return e.Backend.WriteServerFile(k, d.IniName, out)
}
