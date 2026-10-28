package core

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// This file runs the PRIVATE PostgreSQL instance the notification server
// (NTF) needs - it is the only SimpleX server whose store is a database
// rather than a local file. The design keeps everything unprivileged and
// self-contained, in the spirit of the rest of sxops:
//
//   - a data directory under the sxops root (no system service, no root);
//   - TCP is disabled entirely - the server listens on a unix socket in
//     its own 0700 directory, so only the user's own processes can reach
//     it (that is the whole authentication model - no password to leak);
//   - the process is started detached via pg_ctl and survives sxops
//     exiting, exactly like the managed servers do;
//   - the NTF server connects with a libpq connection string that points
//     at that socket and runs its own schema migrations on first start.

const (
	ntfDBName   = "ntf_server_store"
	ntfDBUser   = "sxops_ntf"
	ntfDBSchema = "ntf_server"
)

// pgInstallHint is appended to every "not found" error: sxops only needs
// the binaries, never the system service.
const pgInstallHint = "Install it in a terminal:  sudo apt install postgresql\n" +
	"(on Fedora: sudo dnf install postgresql-server; Arch: sudo pacman -S postgresql),\n" +
	"then press Start again - sxops runs its own private instance, the\n" +
	"system PostgreSQL service is never touched."

// FindPostgresBinDir locates the PostgreSQL server binaries: initdb,
// postgres, pg_ctl and psql. Distributions keep them out of PATH in
// versioned directories (/usr/lib/postgresql/<v>/bin on Debian/Ubuntu),
// so PATH is checked first and the versioned directories after it,
// preferring the newest version.
func FindPostgresBinDir() (string, error) {
	var candidates []string
	if p, err := exec.LookPath("initdb"); err == nil {
		candidates = append(candidates, filepath.Dir(p))
	}
	if dirs, err := filepath.Glob("/usr/lib/postgresql/*/bin"); err == nil {
		candidates = append(candidates, dirs...)
	}
	// glob returns lexical order; reversed, 17 sorts before 9.6
	sort.Sort(sort.Reverse(sort.StringSlice(candidates)))
	seen := map[string]bool{}
	for _, dir := range candidates {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if pgBinDirUsable(dir) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("PostgreSQL was not found (needed by the notification server).\n%s", pgInstallHint)
}

// pgBinDirUsable reports whether dir holds the full server tool set.
func pgBinDirUsable(dir string) bool {
	for _, b := range []string{"initdb", "postgres", "pg_ctl", "psql"} {
		if _, err := exec.LookPath(filepath.Join(dir, b)); err != nil {
			return false
		}
	}
	return true
}

// pgQueryEscape percent-encodes a path for use as the host= parameter of
// a libpq connection URI. url.QueryEscape escapes "/" as %2F (correct -
// the host parameter is a single value, not a path list) but leaves
// spaces as "+" (a form-encoding artifact libpq does not decode), so the
// pluses are converted to %20 by hand.
func pgQueryEscape(path string) string {
	return strings.ReplaceAll(url.QueryEscape(path), "+", "%20")
}

// ntfDBConnStr returns the [STORE_LOG] db_connection value for the NTF
// server INI: the user's advanced override when set, otherwise the
// connection string of the managed private instance ("" when PostgreSQL
// is not installed - the NTF start path surfaces the install hint then).
func ntfDBConnStr(state *StateStore, paths *Paths) string {
	if s := state.Settings(); s.NTFDBConnection != "" {
		return s.NTFDBConnection
	}
	if _, err := FindPostgresBinDir(); err != nil {
		return ""
	}
	return "postgresql://" + ntfDBUser + "@/" + ntfDBName +
		"?host=" + pgQueryEscape(paths.PostgresSockDir())
}

// managedPostgresRunning reports whether the private instance is up.
func (e *Engine) managedPostgresRunning(binDir, dataDir string) bool {
	cmd := exec.Command(filepath.Join(binDir, "pg_ctl"), "-D", dataDir, "status")
	return cmd.Run() == nil
}

// ensureManagedPostgres makes the private instance ready to serve the
// NTF server: initializes the data directory on first use, starts the
// server when it is not running (detached - it survives sxops exiting)
// and creates the NTF database once. It is idempotent and fast when
// everything is already up (two exec calls), so it runs before every
// NTF server start, including crash auto-restarts.
func (e *Engine) ensureManagedPostgres() error {
	binDir, err := FindPostgresBinDir()
	if err != nil {
		return err
	}
	dataDir := e.Paths.PostgresDataDir()
	sockDir := e.Paths.PostgresSockDir()
	psql := filepath.Join(binDir, "psql")

	if _, err := os.Stat(filepath.Join(dataDir, "PG_VERSION")); err != nil {
		// First use (or a half-written directory from an interrupted
		// initdb - initdb insists on an empty target, so it is reset).
		if _, err := os.Stat(dataDir); err == nil {
			e.Log.Detail("resetting an incomplete PostgreSQL data directory")
			_ = os.RemoveAll(dataDir)
		}
		if err := os.MkdirAll(filepath.Dir(dataDir), 0o700); err != nil {
			return err
		}
		e.Log.Step("Initializing the private PostgreSQL database (for the notification server)")
		out, err := CommandOutput(filepath.Join(binDir, "initdb"), []string{
			"-D", dataDir,
			"-U", ntfDBUser,
			"--auth-local=trust",
			"--auth-host=reject",
			"--encoding=UTF8",
			"--locale=C",
		}, nil, 90*time.Second)
		if err != nil {
			return fmt.Errorf("initializing PostgreSQL failed: %v\n%s", err, truncate(out, 800))
		}
	}

	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		return err
	}
	if !e.managedPostgresRunning(binDir, dataDir) {
		e.Log.Step("Starting the private PostgreSQL database")
		out, err := CommandOutput(filepath.Join(binDir, "pg_ctl"), []string{
			"-D", dataDir, "-w", "-t", "30",
			"-l", e.Paths.PostgresLogPath(),
			"-o", "-k " + sockDir + " -c listen_addresses=",
			"start",
		}, nil, 60*time.Second)
		if err != nil {
			logTail, _ := tailFile(e.Paths.PostgresLogPath(), 15)
			return fmt.Errorf("starting PostgreSQL failed: %v\n%s%s\n(log: %s)",
				err, truncate(out, 400), logTail, e.Paths.PostgresLogPath())
		}
	}

	// Create the NTF database once (idempotent). "psql -tAc" prints the
	// scalar result without headers - "1" means it already exists.
	out, err := CommandOutput(psql, []string{
		"-h", sockDir, "-U", ntfDBUser, "-d", "postgres",
		"-tAc", "SELECT 1 FROM pg_database WHERE datname='" + ntfDBName + "'",
	}, nil, 15*time.Second)
	if err != nil {
		return fmt.Errorf("PostgreSQL is not answering on its private socket (%s): %v\n%s",
			filepath.Join(sockDir, ".s.PGSQL.5432"), err, truncate(out, 400))
	}
	if strings.TrimSpace(out) != "1" {
		out, err := CommandOutput(psql, []string{
			"-h", sockDir, "-U", ntfDBUser, "-d", "postgres",
			"-c", "CREATE DATABASE " + ntfDBName,
		}, nil, 30*time.Second)
		if err != nil {
			return fmt.Errorf("creating the notification server database failed: %v\n%s", err, truncate(out, 400))
		}
	}
	// The server never creates its own schema (createSchema is always
	// false when the options come from the INI - it exits with "schema
	// does not exist" instead), so the schema is part of the provisioning.
	// IF NOT EXISTS keeps this idempotent; the server's own migrations
	// (confirmMigrations = MCYesUp) then create the tables inside it.
	out, err = CommandOutput(psql, []string{
		"-h", sockDir, "-U", ntfDBUser, "-d", ntfDBName,
		"-c", "CREATE SCHEMA IF NOT EXISTS " + ntfDBSchema + " AUTHORIZATION " + ntfDBUser,
	}, nil, 30*time.Second)
	if err != nil {
		return fmt.Errorf("creating the notification server schema failed: %v\n%s", err, truncate(out, 400))
	}
	return nil
}

// stopManagedPostgres stops the private instance (fast mode: running
// transactions are aborted, the data stays consistent). The data is kept
// so re-enabling the notification server is quick.
func (e *Engine) stopManagedPostgres() error {
	binDir, err := FindPostgresBinDir()
	if err != nil {
		return nil // PostgreSQL not installed - nothing to stop
	}
	dataDir := e.Paths.PostgresDataDir()
	if !e.managedPostgresRunning(binDir, dataDir) {
		return nil
	}
	out, err := CommandOutput(filepath.Join(binDir, "pg_ctl"), []string{
		"-D", dataDir, "stop", "-m", "fast", "-w", "-t", "30",
	}, nil, 60*time.Second)
	if err != nil {
		return fmt.Errorf("stopping PostgreSQL failed: %v\n%s", err, truncate(out, 400))
	}
	return nil
}

// preStartServer runs immediately before a supervised process spawns.
// The NTF server cannot run without its database: rather than dying with
// the server's own cryptic migration error, sxops brings the private
// PostgreSQL instance up here - one hook covers every start path (the
// dashboard Start button, Start all, the autostart daemon, crash
// auto-restart, updates). An external database configured via the
// advanced NTFDBConnection setting is deliberately left alone.
func (e *Engine) preStartServer(k Kind) error {
	if k != KindNTF || !e.State.Server(k).Installed {
		return nil
	}
	if e.State.Settings().NTFDBConnection != "" {
		return nil // external database - not ours to manage
	}
	if ntfDBConnStr(e.State, e.Paths) == "" {
		return fmt.Errorf("%s", pgInstallHint)
	}
	return e.ensureManagedPostgres()
}
