package core

import (
        "encoding/json"
        "os"
        "sync"
        "time"
)

// State is the persisted sxops state document.
type State struct {
        Version   int                 `json:"version"`
        Settings  Settings            `json:"settings"`
        Servers   map[Kind]*ServerRec `json:"servers"`
        Tor       TorRec              `json:"tor"`
        Provision bool                `json:"provisioned"` // wizard completed at least once
}

// Settings are user preferences shared by all components.
type Settings struct {
        // PublicHost is an optional public hostname/IP to advertise in addresses
        // alongside the onion hostname. Empty = onion-only.
        PublicHost string `json:"public_host,omitempty"`
        // PublicPort is the public SMP port (only used with PublicHost).
        PublicPort int `json:"public_port,omitempty"`
        // PasswordProtect enables basic auth for queue/file creation.
        PasswordProtect bool   `json:"password_protect"`
        QueuePassword   string `json:"queue_password,omitempty"`
        // XFTP settings.
        XFTPQuotaGB     int `json:"xftp_quota_gb,omitempty"`
        XFTPExpireHours int `json:"xftp_expire_hours,omitempty"` // files deleted after this many hours (0 = default 48)
        // Relay settings.
        RelayName  string `json:"relay_name"`
        RelayImage string `json:"relay_image,omitempty"`
        RelayWebDomain  string `json:"relay_web_domain,omitempty"`
        RelayWebEnabled bool   `json:"relay_web_enabled"`
        // Networking.
        EnableInfoPages bool `json:"enable_info_pages"` // [WEB] http info page per server
        SingleHopOnion  bool `json:"single_hop_onion"`   // tor single-hop mode (lower latency)
        AutoRestart     bool `json:"auto_restart"`       // restart crashed servers
        // ServerStats (v1.6.39) controls the daily statistics
        // collection: "off" (default), "smp", "xftp" or "both". The
        // upstream switch is [STORE_LOG] log_stats in each server INI
        // (on = the server collects daily aggregate totals into its
        // LOCAL stats log; the public info page shows only a "Stats
        // enabled" line, never the numbers). Before v1.6.39 sxops
        // forced it on for the SMP server; the setting is now explicit
        // and off by default. The NTF server is not covered (its stats
        // feed a local log only).
        ServerStats string `json:"server_stats,omitempty"`
        // Tor bridges (for censored networks): one bridge line per entry,
        // e.g. "obfs4 203.0.113.1:443 cert=... iat-mode=0" or plain "203.0.113.1:443".
        // Empty = no bridges (direct connection to the Tor network).
        TorBridges []string `json:"tor_bridges,omitempty"`
        // EnableNTF turns on the optional notification server. sxops then
        // sets up everything it needs - including a private, unprivileged
        // PostgreSQL instance (the NTF token store is the only SimpleX
        // store that is a database rather than a local file).
        EnableNTF bool `json:"enable_ntf,omitempty"`
        // NTFDBConnection overrides the managed PostgreSQL connection with
        // an external database (advanced; empty = use the managed one).
        NTFDBConnection string `json:"ntf_db_connection,omitempty"`
        // Release channel: "stable" or "pre" (pre-releases allowed).
        ReleaseChannel string `json:"release_channel"`
        // Optional GitHub API token to avoid rate limits.
        GitHubToken string `json:"github_token,omitempty"`
}

// ServerRec is the installed record for one server kind.
type ServerRec struct {
        Kind       Kind   `json:"kind"`
        Enabled    bool   `json:"enabled"`
        Installed  bool   `json:"installed"`
        BinVersion string `json:"bin_version,omitempty"` // e.g. "SMP server v6.5.0.17 / ba6af65"
        BinTag     string `json:"bin_tag,omitempty"`     // release tag it came from
        BinSHA256  string `json:"bin_sha256,omitempty"`
        // Port is the ADVERTISED port used in the client-facing address
        // (and the onion service virtual port) - it stays at the canonical
        // value (5223/5225/5227) so addresses never change.
        Port int `json:"port"`
        // LocalPort is the port the server actually binds on 127.0.0.1.
        // It differs from Port only when the canonical port was already
        // taken by another program at start time - the onion service then
        // maps the virtual port to this local one and clients are not
        // affected. 0 = same as Port.
        LocalPort    int    `json:"local_port,omitempty"`
        ControlPort  int    `json:"control_port,omitempty"`
        WebPort      int    `json:"web_port,omitempty"`
        // WebHttpsPort is the local port of the SMP server's embedded
        // HTTPS web page ([WEB] https): the official invite/link landing
        // pages served to browsers. The onion service maps its virtual
        // port 443 onto it. 0 = not enabled (XFTP/NTF never have one).
        WebHttpsPort int    `json:"web_https_port,omitempty"`
        Fingerprint  string `json:"fingerprint,omitempty"`
        OnionHost    string `json:"onion_host,omitempty"`
        ControlAdmin string `json:"control_admin,omitempty"`
        ControlUser  string `json:"control_user,omitempty"`
        // SeenQueues records when sxops first listed each queue (SMP) /
        // file recipient (XFTP) - RFC3339. The SMP server does not record
        // queue creation times anywhere (its store log has no timestamps;
        // queue updatedAt is day-granularity at best), so the first-seen
        // time is the only per-queue timestamp sxops can show. (Bans are
        // NOT remembered here any more - the server's own store log
        // journals every block with its reason and notice, and the admin
        // panel derives the ban list from it.)
        SeenQueues map[string]string `json:"seen_queues,omitempty"`
}

// TorRec records managed Tor state.
type TorRec struct {
        Managed     bool `json:"managed"`
        SocksPort   int  `json:"socks_port"`
        ControlPort int  `json:"control_port"`
        OnionReady  bool `json:"onion_ready"`
        // ClientSocksPort/ClientControlPort are the ports of the separate
        // client-only Tor instance that runs in single-hop mode (the service
        // instance must run with SocksPort 0 there). 0 = single instance.
        ClientSocksPort   int `json:"client_socks_port,omitempty"`
        ClientControlPort int `json:"client_control_port,omitempty"`
        // ConnectFailed records that the last attempt to connect Tor to
        // the network failed (ConnectError holds why). The UI shows a
        // "Retry connection" button only in that case; otherwise the
        // button reads "Connect".
        ConnectFailed bool   `json:"connect_failed"`
        ConnectError  string `json:"connect_error,omitempty"`
        // RelaySocks is the Tor SOCKS port the RUNNING chat relay was
        // started with (-1 = it needs no SOCKS proxy at all, 0 = not
        // recorded yet, e.g. a relay adopted from a pre-1.5.6 sxops). The
        // Statuses watchdog restarts the relay when the effective client
        // port (ClientSocksPortOf) drifts from the recorded one - a relay
        // holding a dead SOCKS port stays "running" but never receives
        // anything, so every app-side relay test times out at
        // "Wait response".
        RelaySocks int `json:"relay_socks,omitempty"`
}

// StateStore guards access to the state file.
type StateStore struct {
        mu    sync.Mutex
        path  string
        state *State
}

// State schema version.
//
// 1 - original
// 2 - v1.6.8: the XFTP storage quota default changed from 10 GB to
//     "off" (no limit); states still carrying the old silent default
//     are migrated once on load.
const stateVersion = 2

// ServerStatsEnabled reports whether the daily statistics are on for
// the server kind per the ServerStats setting ("smp" / "xftp" /
// "both"). Everything else - including the empty values old states
// carry - is off.
func (s Settings) ServerStatsEnabled(k Kind) bool {
        switch s.ServerStats {
        case "smp":
                return k == KindSMP
        case "xftp":
                return k == KindXFTP
        case "both":
                return k == KindSMP || k == KindXFTP
        }
        return false
}

// defaultSettings assembles the fresh-install settings.
func defaultSettings() Settings {
        return Settings{
                RelayName:       "My Relay",
                XFTPQuotaGB:     0, // 0 = off (no storage quota) since v1.6.8
                XFTPExpireHours: 48,
                EnableInfoPages: true,
                AutoRestart:     true,
                ReleaseChannel:  "stable",
                ServerStats:     "off", // v1.6.39: daily stats collection off unless chosen
        }
}

// NewStateStore loads state from disk, creating defaults when absent.
func NewStateStore(paths *Paths) (*StateStore, error) {
        st := &StateStore{path: paths.StateFile()}
        data, err := os.ReadFile(st.path)
        if err == nil {
                var s State
                if json.Unmarshal(data, &s) == nil {
                        st.state = &s
                        st.applyDefaults()
                        return st, nil
                }
                // Corrupt state: keep a backup and start fresh.
                _ = os.Rename(st.path, st.path+".corrupt")
        }
        st.state = &State{
                Version:  stateVersion,
                Settings: defaultSettings(),
                Servers:  map[Kind]*ServerRec{},
        }
        if err := st.Save(); err != nil {
                return nil, err
        }
        return st, nil
}

func (st *StateStore) applyDefaults() {
        if st.state.Settings.RelayName == "" {
                st.state.Settings.RelayName = "My Relay"
        }
        if st.state.Settings.XFTPExpireHours == 0 {
                st.state.Settings.XFTPExpireHours = 48
        }
        if st.state.Settings.XFTPQuotaGB < 0 {
                st.state.Settings.XFTPQuotaGB = 0 // negative = off
        }
        if st.state.Settings.ReleaseChannel == "" {
                st.state.Settings.ReleaseChannel = "stable"
        }
        // v1.6.39: any unknown/empty stats value reads as "off" (the
        // setting predates this version; old states have no field).
        switch st.state.Settings.ServerStats {
        case "smp", "xftp", "both":
        default:
                st.state.Settings.ServerStats = "off"
        }
        if st.state.Servers == nil {
                st.state.Servers = map[Kind]*ServerRec{}
        }
        // v1.6.8 one-time migration: 10 GB was the silent v1.6.7 default
        // (applyDefaults coerced 0 -> 10 on every load), so an old state
        // carrying 10 almost never holds a deliberate choice - and the new
        // default is "off". A quota explicitly picked in the v1.6.8 settings
        // panel (any value, including 10) is version 2 and never touched.
        if st.state.Version < 2 {
                if st.state.Version < 1 {
                        st.state.Version = 1
                }
                if st.state.Settings.XFTPQuotaGB == 10 {
                        st.state.Settings.XFTPQuotaGB = 0
                }
                st.state.Version = 2
        }
}

// Get returns the current state (read-only copy semantics: callers may
// mutate under With()).
func (st *StateStore) Get() State {
        st.mu.Lock()
        defer st.mu.Unlock()
        return *st.state
}

// With runs fn with a deep-ish copy-on-write style mutation under lock.
func (st *StateStore) With(fn func(s *State)) error {
        st.mu.Lock()
        fn(st.state)
        st.mu.Unlock()
        return st.Save()
}

// Server returns the ServerRec for a kind, creating it when missing.
func (st *StateStore) Server(k Kind) *ServerRec {
        st.mu.Lock()
        defer st.mu.Unlock()
        return st.serverLocked(k)
}

// ServerCopy returns a race-free deep copy of a server record (including
// the seen-queue stamps) for UI rendering off the writer path.
func (st *StateStore) ServerCopy(k Kind) ServerRec {
        st.mu.Lock()
        defer st.mu.Unlock()
        rec := st.serverLocked(k)
        cp := *rec
        if rec.SeenQueues != nil {
                cp.SeenQueues = make(map[string]string, len(rec.SeenQueues))
                for id, at := range rec.SeenQueues {
                        cp.SeenQueues[id] = at
                }
        }
        return cp
}

func (st *StateStore) serverLocked(k Kind) *ServerRec {
        rec, ok := st.state.Servers[k]
        if !ok {
                rec = &ServerRec{Kind: k, Enabled: true}
                st.state.Servers[k] = rec
        }
        return rec
}

// UpdateServer mutates a server record and persists.
func (st *StateStore) UpdateServer(k Kind, fn func(rec *ServerRec)) error {
        return st.With(func(s *State) {
                fn(st.serverLocked(k))
        })
}

// noteQueuesSeen stamps missing IDs with the current time (RFC3339) and
// persists only when something was added. Called from ListQueues so the
// admin list gains an identifying timestamp per queue; existing entries
// are never re-stamped, and entries are never pruned - the store log is
// read as a tail, so an absent ID does not mean it was deleted.
func (st *StateStore) noteQueuesSeen(k Kind, ids []QueueInfo) {
        st.mu.Lock()
        rec := st.serverLocked(k)
        added := false
        now := time.Now().UTC().Format(time.RFC3339)
        if rec.SeenQueues == nil {
                rec.SeenQueues = map[string]string{}
        }
        for _, q := range ids {
                if q.ID != "" {
                        if _, ok := rec.SeenQueues[q.ID]; !ok {
                                rec.SeenQueues[q.ID] = now
                                added = true
                        }
                }
        }
        st.mu.Unlock()
        if added {
                _ = st.Save()
        }
}

// Settings returns current settings snapshot.
func (st *StateStore) Settings() Settings {
        st.mu.Lock()
        defer st.mu.Unlock()
        return st.state.Settings
}

// SetSettings replaces settings and persists.
func (st *StateStore) SetSettings(s Settings) error {
        return st.With(func(st_ *State) { st_.Settings = s })
}

// Save writes state atomically.
func (st *StateStore) Save() error {
        st.mu.Lock()
        defer st.mu.Unlock()
        if st.state == nil {
                return nil
        }
        data, err := json.MarshalIndent(st.state, "", "  ")
        if err != nil {
                return err
        }
        tmp := st.path + ".tmp"
        if err := os.WriteFile(tmp, data, 0o600); err != nil {
                return err
        }
        return os.Rename(tmp, st.path)
}
