package core

import (
        "bufio"
        "fmt"
        "net"
        "strings"
        "time"
)

// ControlClient speaks the simplexmq control port protocol (validated
// against smp-server v6.5.0 / xftp-server v6.5.0):
//
//      connect TCP (localhost only), read banner (TWO lines)
//      auth <password>            -> "Current role is CPRAdmin" / CPRUser / error
//      stats | server-info | clients | sockets | ... -> multi-line response
//      quit
//
// The port has no response terminator: the end of each response is
// detected by a short inactivity window (see exec).
type ControlClient struct {
        Kind Kind
        Port int
        Auth string // admin or user password

        conn   net.Conn
        reader *bufio.Reader
        role   string
}

// ControlCommandSet lists valid commands per server protocol flavor.
// "suspend"/"resume" are parsed by the servers but answered with
// "not implemented" (verified against v6.5.0 and stable) and are
// therefore NOT listed - nothing in sxops may call them. "save" was
// removed in v1.6.45: it only flushes the server's state to disk with
// no visible effect, so the button that sent it was retired with it.
var controlCommands = map[string][]string{
        "smp":  {"stats", "stats-rts", "server-info", "clients", "sockets", "socket-threads", "threads", "delete", "status", "block", "unblock"},
        "xftp": {"stats-rts", "delete", "block"},
        "ntf":  {"stats", "stats-rts", "server-info"},
}

// ControlSupported reports whether a command is accepted by a kind.
func ControlSupported(k Kind, command string) bool {
        proto := Def(k).ControlProto
        if proto == "" {
                return false
        }
        for _, c := range controlCommands[proto] {
                if c == command {
                        return true
                }
        }
        return false
}

// Dial connects and authenticates.
func (c *ControlClient) Dial(timeout time.Duration) error {
        conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", c.Port), timeout)
        if err != nil {
                return fmt.Errorf("control port %d not reachable: %v", c.Port, err)
        }
        c.conn = conn
        c.reader = bufio.NewReader(conn)
        // Drain the WHOLE banner - the servers print two lines
        // ("<kind> server control port" + "'help' for supported commands");
        // leaving the second line in the buffer would pollute the first
        // command's response.
        c.conn.SetReadDeadline(time.Now().Add(timeout))
        for {
                if _, err := c.reader.ReadString('\n'); err != nil {
                        break
                }
                c.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
        }
        if c.Auth != "" {
                resp, err := c.exec("auth " + c.Auth)
                if err != nil {
                        conn.Close()
                        return err
                }
                c.role = strings.TrimSpace(resp)
                // A wrong password does not produce an ERR line - the server
                // answers "Current role is CPRNone" and only rejects the next
                // command. Fail right here with a clear message instead.
                if strings.Contains(c.role, "CPRNone") {
                        conn.Close()
                        return fmt.Errorf("control port auth failed: the control password was rejected")
                }
                if strings.Contains(c.role, "ERR") || strings.Contains(strings.ToLower(c.role), "error") {
                        conn.Close()
                        return fmt.Errorf("control port auth failed: %s", c.role)
                }
        }
        return nil
}

// exec sends one command and reads until a short quiet period.
// The first response line may take up to 4 seconds (slow command start);
// every following line resets a ~600ms inactivity window, so buffered
// multi-line reports are collected completely while single-line replies
// return near-instantly.
func (c *ControlClient) exec(command string) (string, error) {
        if _, err := c.conn.Write([]byte(command + "\n")); err != nil {
                return "", err
        }
        tail := 600 * time.Millisecond
        var sb strings.Builder
        first := true
        for {
                if first {
                        c.conn.SetReadDeadline(time.Now().Add(4 * time.Second))
                } else {
                        c.conn.SetReadDeadline(time.Now().Add(tail))
                }
                line, err := c.reader.ReadString('\n')
                if line != "" {
                        sb.WriteString(line)
                        first = false
                }
                if err != nil {
                        if sb.Len() == 0 {
                                // no response at all within 4s - the command (or the
                                // connection) is dead; silently returning an empty
                                // success would hide the failure from callers
                                return "", fmt.Errorf("no response from the control port: %v", err)
                        }
                        break
                }
        }
        return sb.String(), nil
}

// Run executes a command and returns the raw multi-line response.
func (c *ControlClient) Run(command string) (string, error) {
        if !ControlSupported(c.Kind, command) {
                return "", fmt.Errorf("%s control port does not support %q", Def(c.Kind).Name, command)
        }
        return c.exec(command)
}

// Close sends quit and closes the connection.
func (c *ControlClient) Close() {
        if c.conn != nil {
                _, _ = c.conn.Write([]byte("quit\n"))
                c.conn.Close()
        }
}

// Stats is a parsed `stats` response.
type Stats map[string]string

// Get returns a value ("0" when missing). The servers capitalize some
// keys differently ("Clients" vs "clients"), so the lookup is
// case-insensitive.
func (s Stats) Get(key string) string {
        if v, ok := s[key]; ok {
                return v
        }
        lk := strings.ToLower(key)
        for k, v := range s {
                if strings.ToLower(k) == lk {
                        return v
                }
        }
        return "0"
}

// FetchStats connects, authenticates and returns parsed stats.
func FetchStats(k Kind, rec *ServerRec) (Stats, error) {
        if rec.ControlPort == 0 {
                return nil, fmt.Errorf("control port not configured for %s", Def(k).Name)
        }
        c := &ControlClient{Kind: k, Port: rec.ControlPort, Auth: rec.ControlAdmin}
        if c.Auth == "" {
                c.Auth = rec.ControlUser
        }
        if err := c.Dial(5 * time.Second); err != nil {
                return nil, err
        }
        defer c.Close()
        raw, err := c.Run("stats")
        if err != nil {
                return nil, err
        }
        if strings.Contains(raw, "bad ControlProtocol") || strings.Contains(raw, "ERR") {
                return nil, fmt.Errorf("stats not supported: %s", strings.TrimSpace(raw))
        }
        st := Stats{}
        for _, line := range strings.Split(raw, "\n") {
                line = strings.TrimSpace(line)
                if i := strings.Index(line, ": "); i > 0 {
                        st[line[:i]] = strings.TrimSpace(line[i+2:])
                }
        }
        return st, nil
}

// FetchRaw connects and returns a raw command response (admin panel).
func FetchRaw(k Kind, rec *ServerRec, command, arg string) (string, error) {
        if rec.ControlPort == 0 {
                return "", fmt.Errorf("control port not configured for %s", Def(k).Name)
        }
        // reject unsupported commands BEFORE connecting - the admin panel
        // probes capabilities through this function and must not open (and
        // authenticate on) connections for commands that can never run
        if !ControlSupported(k, command) {
                return "", fmt.Errorf("%s control port does not support %q", Def(k).Name, command)
        }
        c := &ControlClient{Kind: k, Port: rec.ControlPort, Auth: rec.ControlAdmin}
        if err := c.Dial(5 * time.Second); err != nil {
                return "", err
        }
        defer c.Close()
        cmdline := command
        if arg != "" {
                cmdline = command + " " + arg
        }
        return c.exec(cmdline)
}

// ClientsRow is one row of the `clients` CSV output.
type ClientsRow struct {
        ClientID      string
        SessionID     string
        Connected     string
        CreatedAt     string
        RcvActiveAt   string
        SndActiveAt   string
        Age           string
        Subscriptions string
}

// ParseClients parses the clients CSV (header + rows).
func ParseClients(raw string) []ClientsRow {
        var out []ClientsRow
        lines := strings.Split(strings.TrimSpace(raw), "\n")
        if len(lines) == 0 {
                return nil
        }
        // find header
        start := -1
        for i, l := range lines {
                if strings.HasPrefix(l, "clientId,") {
                        start = i + 1
                        break
                }
        }
        if start < 0 {
                return nil
        }
        for _, l := range lines[start:] {
                l = strings.TrimSpace(l)
                if l == "" || strings.HasPrefix(l, "error") {
                        continue
                }
                f := strings.Split(l, ",")
                for len(f) < 8 {
                        f = append(f, "")
                }
                out = append(out, ClientsRow{
                        ClientID:      f[0],
                        SessionID:     f[1],
                        Connected:     f[2],
                        CreatedAt:     f[3],
                        RcvActiveAt:   f[4],
                        SndActiveAt:   f[5],
                        Age:           f[6],
                        Subscriptions: f[7],
                })
        }
        return out
}

// ---- client moderation (official simplexmq methods) ----

// Ban reasons understood by the servers' blocking-info wire format
// (simplexmq BlockingInfo): "reason=spam" or "reason=content".
const (
        BanReasonSpam    = "spam"
        BanReasonContent = "content"
)

// ClientNotice is the official simplexmq ban notice (Simplex.Messaging.
// Protocol.Types.ClientNotice) that can travel to the banned client's app
// together with the block: the app receives ERR BLOCKED, remembers the
// notice for this server and then REFUSES TO CREATE NEW LINKS via this
// server while the notice lasts - the moderation measure the official
// SimpleX servers use against returning spammers. Enforcement is in the
// official apps (SMP protocol versions >= 18); older apps ignore the
// notice and only see the blocked queue.
type ClientNotice struct {
        // TTL is the notice lifetime in seconds; nil = no expiry (the app
        // refuses new links until the queue is unblocked and used again).
        TTL *int64
}

// NewClientNotice builds the official ban notice for a lifetime in
// seconds (0 = no expiry).
func NewClientNotice(ttlSeconds int64) *ClientNotice {
        if ttlSeconds == 0 {
                return &ClientNotice{TTL: nil}
        }
        return &ClientNotice{TTL: &ttlSeconds}
}

// String renders the notice as the JSON the servers' BlockingInfo parser
// expects: {"ttl":null} (no expiry) or {"ttl":86400}. It must contain no
// spaces - the control port splits commands on spaces.
func (n *ClientNotice) String() string {
        if n == nil {
                return ""
        }
        if n.TTL == nil {
                return `{"ttl":null}`
        }
        return fmt.Sprintf(`{"ttl":%d}`, *n.TTL)
}

// blockingInfoArg builds the <blockingInfo> argument for the control-port
// "block" command. The server parser accepts exactly:
//
//      reason=spam | reason=content | reason=<..>[,notice=<json>]
//
// so free text cannot be sent; sxops maps the user's choice to one of the
// two documented reasons and appends the official ban notice
// (ClientNotice JSON) when one was chosen.
func blockingInfoArg(reason string, notice *ClientNotice) string {
        arg := "reason=spam"
        if strings.EqualFold(strings.TrimSpace(reason), BanReasonContent) {
                arg = "reason=content"
        }
        if s := notice.String(); s != "" {
                arg += ",notice=" + s
        }
        return arg
}

// controlResponseError inspects a raw control-port response for
// server-side error lines ("error: ...", "error, ..." or a bare "AUTH"
// role rejection) and turns them into a friendly error. k selects the
// unknown-ID wording: the SMP server resolves queues by sender, link or
// recipient ID, while the XFTP server resolves files through the
// recipient map only (upstream getFile answers AUTH for unknown IDs).
func controlResponseError(k Kind, raw string) error {
        for _, line := range strings.Split(raw, "\n") {
                line = strings.TrimSpace(line)
                if line == "" {
                        continue
                }
                if line == "AUTH" {
                        return fmt.Errorf("the server rejected the command (wrong control password)")
                }
                if line == "error: AUTH" {
                        if k == KindXFTP {
                                return fmt.Errorf("the server has no file for this file ID (it may be wrong, already deleted, or every recipient already downloaded it)")
                        }
                        return fmt.Errorf("the server has no queue for this client ID (it may be wrong, or already deleted)")
                }
                if strings.HasPrefix(line, "error: ") {
                        return fmt.Errorf("server: %s", strings.TrimPrefix(line, "error: "))
                }
                if strings.HasPrefix(strings.ToLower(line), "error, ") {
                        return fmt.Errorf("server: %s", line)
                }
        }
        return nil
}

// checkClientID validates a moderator-supplied client (sender) ID.
func checkClientID(clientID string) error {
        clientID = strings.TrimSpace(clientID)
        if clientID == "" {
                return fmt.Errorf("no client ID entered - paste the client's queue ID first")
        }
        if strings.ContainsAny(clientID, " \t\r\n") {
                return fmt.Errorf("a client ID cannot contain spaces - copy the whole ID")
        }
        if len(clientID) > 256 {
                return fmt.Errorf("this does not look like a client ID (too long)")
        }
        return nil
}

// BanClient blocks the client's queue on the server - the official
// SimpleX moderation method. The queue stops accepting the banned
// client's operations and the ban is reversible with UnbanClient; reason
// is recorded by the server with the block and must be one of
// BanReasonSpam / BanReasonContent. A non-nil notice additionally sends
// the official ban notice to the banned client's app (immediately when it
// is connected, otherwise on its next use of the queue), and the app then
// refuses to create new links via this server for the notice's lifetime
// (nil TTL = until the queue is unblocked) - how the official SimpleX
// servers stop returning spammers. clientID may be the queue ID or a
// connection link ID (the server resolves both to the same queue).
func BanClient(k Kind, rec *ServerRec, clientID, reason string, notice *ClientNotice) (string, error) {
        if err := checkClientID(clientID); err != nil {
                return "", err
        }
        if !ControlSupported(k, "block") {
                return "", fmt.Errorf("%s does not support blocking clients", Def(k).Name)
        }
        // wire format: block <senderId|linkId> <blockingInfo>
        out, err := FetchRaw(k, rec, "block", strings.TrimSpace(clientID)+" "+blockingInfoArg(reason, notice))
        if err != nil {
                return "", err
        }
        if rerr := controlResponseError(k, out); rerr != nil {
                return out, rerr
        }
        return out, nil
}

// UnbanClient removes a block placed earlier.
func UnbanClient(k Kind, rec *ServerRec, clientID string) (string, error) {
        if err := checkClientID(clientID); err != nil {
                return "", err
        }
        if !ControlSupported(k, "unblock") {
                return "", fmt.Errorf("%s does not support unblocking clients", Def(k).Name)
        }
        out, err := FetchRaw(k, rec, "unblock", strings.TrimSpace(clientID))
        if err != nil {
                return "", err
        }
        if rerr := controlResponseError(k, out); rerr != nil {
                return out, rerr
        }
        return out, nil
}

// DeleteClient permanently deletes the client's queue from the server
// (content moderation). This cannot be undone.
func DeleteClient(k Kind, rec *ServerRec, clientID string) (string, error) {
        if err := checkClientID(clientID); err != nil {
                return "", err
        }
        if !ControlSupported(k, "delete") {
                return "", fmt.Errorf("%s does not support deleting clients", Def(k).Name)
        }
        out, err := FetchRaw(k, rec, "delete", strings.TrimSpace(clientID))
        if err != nil {
                return "", err
        }
        if rerr := controlResponseError(k, out); rerr != nil {
                return out, rerr
        }
        return out, nil
}

// ClientStatus asks the server for one client's queue status, including
// blocking info.
func ClientStatus(k Kind, rec *ServerRec, clientID string) (string, error) {
        if err := checkClientID(clientID); err != nil {
                return "", err
        }
        if !ControlSupported(k, "status") {
                return "", fmt.Errorf("%s does not support per-client status", Def(k).Name)
        }
        out, err := FetchRaw(k, rec, "status", strings.TrimSpace(clientID))
        if err != nil {
                return "", err
        }
        if rerr := controlResponseError(k, out); rerr != nil {
                return out, rerr
        }
        return out, nil
}
