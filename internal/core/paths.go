// Package core implements the platform-agnostic engine of sxops:
// server installation, provisioning, supervision, Tor onion services,
// the control-port API client and the chat relay agent manager.
package core

import (
        "os"
        "path/filepath"
)

// Kind identifies a managed SimpleX component.
type Kind string

const (
        KindSMP   Kind = "smp"
        KindXFTP  Kind = "xftp"
        KindNTF   Kind = "ntf"
        KindRelay Kind = "relay"
)

// Paths holds the portable directory layout used by sxops.
// Everything lives under Root so no root privileges are required.
type Paths struct {
        Root string
}

// DefaultRoot returns the per-OS application data root.
func DefaultRoot() (string, error) {
        // XDG data home (also used inside WSL2 on Windows).
        xdg := os.Getenv("XDG_DATA_HOME")
        if xdg != "" {
                return filepath.Join(xdg, "sxops"), nil
        }
        home, err := os.UserHomeDir()
        if err != nil {
                return "", err
        }
        return filepath.Join(home, ".local", "share", "sxops"), nil
}

// NewPaths builds a Paths rooted at root ("" = DefaultRoot).
func NewPaths(root string) (*Paths, error) {
        if root == "" {
                r, err := DefaultRoot()
                if err != nil {
                        return nil, err
                }
                root = r
        }
        return &Paths{Root: root}, nil
}

func (p *Paths) binDir() string     { return filepath.Join(p.Root, "bin") }
func (p *Paths) serversDir() string { return filepath.Join(p.Root, "servers") }

// BinPath is where a managed binary is installed.
func (p *Paths) BinPath(kind Kind) string {
        switch kind {
        case KindSMP:
                return filepath.Join(p.binDir(), "smp-server")
        case KindXFTP:
                return filepath.Join(p.binDir(), "xftp-server")
        case KindNTF:
                return filepath.Join(p.binDir(), "ntf-server")
        case KindRelay:
                return filepath.Join(p.binDir(), "simplex-chat")
        }
        return filepath.Join(p.binDir(), string(kind))
}

// TorBinPath is where a bundled Tor binary would be installed.
func (p *Paths) TorBinPath() string {
        return filepath.Join(p.binDir(), "tor", "tor")
}

// CfgDir / LogDir are the per-server config and state directories that are
// passed to the server binaries via their *_SERVER_CFG_PATH / *_SERVER_LOG_PATH
// environment variables.
func (p *Paths) CfgDir(kind Kind) string {
        return filepath.Join(p.serversDir(), string(kind), "cfg")
}

func (p *Paths) LogDir(kind Kind) string {
        return filepath.Join(p.serversDir(), string(kind), "var")
}

// FilesDir is the XFTP file storage directory.
func (p *Paths) FilesDir() string { return filepath.Join(p.serversDir(), string(KindXFTP), "files") }

// RelayDir is the chat relay database prefix directory.
func (p *Paths) RelayDir() string { return filepath.Join(p.serversDir(), string(KindRelay)) }

// RelayDBPrefix is the -d argument for the chat relay CLI.
func (p *Paths) RelayDBPrefix() string {
        return filepath.Join(p.RelayDir(), "relay")
}

// TestDir holds the throw-away profile used by the app-style server
// self-test (the `/smp test` one-shot CLI runs).
func (p *Paths) TestDir() string { return filepath.Join(p.Root, "test") }

// TestDBPrefix is the -d argument for the self-test CLI profile.
func (p *Paths) TestDBPrefix() string {
        return filepath.Join(p.TestDir(), "servertest")
}

// PostgresDataDir is the data directory of the private PostgreSQL
// instance sxops runs for the notification server (NTF): no root, no
// system service, TCP disabled - it listens on a unix socket only.
func (p *Paths) PostgresDataDir() string {
        return filepath.Join(p.Root, "postgres", "data")
}

// PostgresSockDir is the unix-socket directory of the private
// PostgreSQL instance. 0700 under the sxops root: only the user's own
// processes can reach it (that is the whole auth model - no password).
func (p *Paths) PostgresSockDir() string {
        return filepath.Join(p.Root, "postgres", "sock")
}

// PostgresLogPath is the log file of the private PostgreSQL instance.
func (p *Paths) PostgresLogPath() string {
        return filepath.Join(p.LogsDir(), "postgres.log")
}

// TorDir is the managed Tor data/config directory.
func (p *Paths) TorDir() string { return filepath.Join(p.Root, "tor") }

// TorDataDir is Tor's DataDirectory (must be owned by the tor process user).
func (p *Paths) TorDataDir() string { return filepath.Join(p.TorDir(), "datadir") }

// TorClientDataDir is the DataDirectory of the second, client-only Tor
// instance that runs in single-hop mode (the service instance must run
// with SocksPort 0 there, so sxops needs a separate anonymous client for
// the relay registration and the reachability probes).
func (p *Paths) TorClientDataDir() string { return filepath.Join(p.TorDir(), "client-datadir") }

// TorClientPidFile is the pidfile the client Tor instance writes itself.
func (p *Paths) TorClientPidFile() string { return filepath.Join(p.RunDir(), "tor-client.pid") }

// TorClientLogFile is the notice log of the client Tor instance.
func (p *Paths) TorClientLogFile() string { return filepath.Join(p.LogsDir(), "tor-client.log") }

// TorClientRcPath is the generated torrc of the client Tor instance.
func (p *Paths) TorClientRcPath() string { return filepath.Join(p.TorDir(), "torrc-client") }

// HiddenServiceDir returns the hidden service directory for a kind.
func (p *Paths) HiddenServiceDir(kind Kind) string {
        return filepath.Join(p.TorDir(), "hs", string(kind))
}

// StateFile is the JSON state file path.
func (p *Paths) StateFile() string { return filepath.Join(p.Root, "state.json") }

// VersionsFile pins installed binary hashes and versions.
func (p *Paths) VersionsFile() string { return filepath.Join(p.Root, "versions.json") }

// DownloadsDir is the staging area for release downloads.
func (p *Paths) DownloadsDir() string { return filepath.Join(p.Root, "downloads") }

// LogsDir holds supervisor-captured process logs.
func (p *Paths) LogsDir() string { return filepath.Join(p.Root, "logs") }

// RunDir holds pidfiles of running components.
func (p *Paths) RunDir() string { return filepath.Join(p.Root, "run") }

// PidFile is the pidfile for a supervised component.
func (p *Paths) PidFile(kind Kind) string {
        return filepath.Join(p.RunDir(), string(kind)+".pid")
}

// TorPidFile is the pidfile Tor writes itself (torrc PidFile).
func (p *Paths) TorPidFile() string { return filepath.Join(p.RunDir(), "tor.pid") }

// ProcLogFile is the captured stdout/stderr log for a component.
func (p *Paths) ProcLogFile(kind Kind) string {
        return filepath.Join(p.LogsDir(), string(kind)+".log")
}

// StatsLogFile is the daily statistics CSV the server writes into its
// portable LOG directory when [STORE_LOG] log_stats = on (v1.6.40: the
// Settings note tells the user this exact path). Empty when the kind
// has no stats log (the relay).
func (p *Paths) StatsLogFile(kind Kind) string {
        if name := StatsDailyLogName(kind); name != "" {
                return filepath.Join(p.LogDir(kind), name)
        }
        return ""
}

// TorLogFile is the managed Tor notice log.
func (p *Paths) TorLogFile() string { return filepath.Join(p.LogsDir(), "tor.log") }

// Ensure creates the directory skeleton.
func (p *Paths) Ensure() error {
        dirs := []string{
                p.Root, p.binDir(), p.serversDir(), p.LogsDir(), p.DownloadsDir(), p.RunDir(),
                p.TorDir(), p.TorDataDir(), p.TorClientDataDir(),
                filepath.Join(p.TorDir(), "hs"),
                filepath.Join(p.Root, "postgres"), p.PostgresSockDir(),
        }
        for _, k := range []Kind{KindSMP, KindXFTP, KindNTF, KindRelay} {
                dirs = append(dirs, p.CfgDir(k), p.LogDir(k))
        }
        dirs = append(dirs, p.FilesDir(), p.RelayDir())
        for _, d := range dirs {
                if err := os.MkdirAll(d, 0o700); err != nil {
                        return err
                }
        }
        return nil
}
