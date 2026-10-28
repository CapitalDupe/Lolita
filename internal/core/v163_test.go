package core

import (
        "os"
        "path/filepath"
        "strconv"
        "strings"
        "testing"
)

// v1.6.3: onion-only links carry no port. The clients' protocol defaults
// (SMP 5223, XFTP 443, NTF 443 - verified against the simplexmq client
// source: defaultSMPClientConfig, "if null port then \"443\"", and the
// notification client's defaultTransport) are mapped as extra VIRTUAL
// ports on the onion services, so portless addresses reach the servers.

func TestClientDefaultPort(t *testing.T) {
        cases := []struct {
                k    Kind
                want int
        }{
                {KindSMP, 5223},
                {KindXFTP, 443},
                {KindNTF, 443},
                {KindRelay, 0},
        }
        for _, c := range cases {
                if got := clientDefaultPort(c.k); got != c.want {
                        t.Errorf("clientDefaultPort(%s) = %d, want %d", c.k, got, c.want)
                }
        }
}

func TestAddressPortlessOnionOnly(t *testing.T) {
        s := Settings{}
        cases := []struct {
                k    Kind
                port int
                want string
        }{
                // canonical ports: the port is simply redundant
                {KindSMP, 5223, "smp://FP@abc.onion"},
                {KindXFTP, 5225, "xftp://FP@abc.onion"},
                {KindNTF, 5227, "ntf://FP@abc.onion"},
                // a legacy non-canonical recorded port must not leak into the
                // link either: the default virtual port is mapped to the same
                // local target, so the portless form works
                {KindSMP, 5224, "smp://FP@abc.onion"},
                {KindXFTP, 443, "xftp://FP@abc.onion"},
                // no port recorded yet
                {KindSMP, 0, "smp://FP@abc.onion"},
        }
        for _, c := range cases {
                rec := &ServerRec{Kind: c.k, Port: c.port, Fingerprint: "FP", OnionHost: "abc.onion"}
                if got := Address(c.k, s, rec); got != c.want {
                        t.Errorf("Address(%s, port %d) = %q, want %q", c.k, c.port, got, c.want)
                }
        }
}

func TestAddressPublicHostStillCarriesPort(t *testing.T) {
        // public-host mode is unchanged: the advertised port stays in the
        // address whenever it differs from the protocol default, because a
        // public host is only reachable under that exact port.
        s := Settings{PublicHost: "srv.example.com"}
        rec := &ServerRec{Kind: KindXFTP, Port: 5225, Fingerprint: "FP"}
        if got := Address(KindXFTP, s, rec); !strings.Contains(got, "srv.example.com") || !strings.Contains(got, ":5225") {
                t.Errorf("public-host XFTP address = %q, want srv.example.com with :5225", got)
        }
        recSMP := &ServerRec{Kind: KindSMP, Port: 5223, Fingerprint: "FP"}
        if got := Address(KindSMP, s, recSMP); strings.Contains(got, ":5223") {
                t.Errorf("public-host SMP address at the default port = %q, want no :5223 suffix", got)
        }
}

func TestGenerateTorrcDefaultVirtualPorts(t *testing.T) {
        eng, paths, _ := newTestEngine(t)
        p := freeTestPorts(t)
        fakeInstalledSMPAt(t, eng, p)
        // XFTP on its canonical port 5225
        if err := eng.State.UpdateServer(KindXFTP, func(r *ServerRec) {
                r.Installed = true
                r.Port = 5225
                r.Fingerprint = "FX"
                r.OnionHost = "xftp.onion"
        }); err != nil {
                t.Fatal(err)
        }
        if err := eng.Tor.GenerateTorrc(); err != nil {
                t.Fatal(err)
        }
        data, err := os.ReadFile(filepath.Join(paths.TorDir(), "torrc"))
        if err != nil {
                t.Fatal(err)
        }
        torrc := string(data)
        // canonical virtual ports stay (already-shared links with the port)
        if !strings.Contains(torrc, "HiddenServicePort "+strconv.Itoa(p.transport)+" 127.0.0.1:"+strconv.Itoa(p.transport)) {
                t.Errorf("torrc must keep the SMP canonical virtual port %d:\n%s", p.transport, torrc)
        }
        if !strings.Contains(torrc, "HiddenServicePort 5225 127.0.0.1:5225") {
                t.Errorf("torrc must keep the XFTP canonical virtual port 5225:\n%s", torrc)
        }
        // protocol-default virtual ports added (portless links)
        if !strings.Contains(torrc, "HiddenServicePort 5223 127.0.0.1:"+strconv.Itoa(p.transport)) {
                t.Errorf("torrc must map the SMP default virtual port 5223 to the local port:\n%s", torrc)
        }
        if !strings.Contains(torrc, "HiddenServicePort 443 127.0.0.1:5225") {
                t.Errorf("torrc must map the XFTP default virtual port 443 to the local port:\n%s", torrc)
        }
        // the detector accepts the fresh layout
        if !eng.Tor.torrcHasDefaultPorts() {
                t.Errorf("torrcHasDefaultPorts must accept the new layout:\n%s", torrc)
        }
}

func TestTorrcHasDefaultPortsDetectsOldLayout(t *testing.T) {
        eng, paths, _ := newTestEngine(t)
        p := freeTestPorts(t)
        fakeInstalledSMPAt(t, eng, p)
        torrcPath := filepath.Join(paths.TorDir(), "torrc")
        // an old (pre-1.6.3) torrc: canonical virtual port only
        old := "HiddenServicePort " + strconv.Itoa(p.transport) + " 127.0.0.1:" + strconv.Itoa(p.transport) + "\n"
        if err := os.WriteFile(torrcPath, []byte(old), 0o600); err != nil {
                t.Fatal(err)
        }
        if eng.Tor.torrcHasDefaultPorts() {
                t.Errorf("torrcHasDefaultPorts must detect the old layout as stale")
        }
        // the migration (Tor not running -> file rewrite only) upgrades it
        if err := eng.State.With(func(st *State) { st.Tor.Managed = true }); err != nil {
                t.Fatal(err)
        }
        eng.EnsurePortlessLinks()
        data, err := os.ReadFile(torrcPath)
        if err != nil {
                t.Fatal(err)
        }
        if !strings.Contains(string(data), "HiddenServicePort 5223 127.0.0.1:"+strconv.Itoa(p.transport)) {
                t.Errorf("migration must add the default virtual port 5223:\n%s", string(data))
        }
        if !eng.Tor.torrcHasDefaultPorts() {
                t.Errorf("migration must leave the torrc detected as current:\n%s", string(data))
        }
}
