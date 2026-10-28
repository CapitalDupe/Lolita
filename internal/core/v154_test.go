package core

import (
        "testing"
)

func TestValidateRelayAddress(t *testing.T) {
        cases := []struct {
                name string
                addr string
                ok   bool
        }{
                {"modern onion link", "https://simplex.chat/contact#/?v=1%2C2&smp=smp%3A%2F%2Fabc%40xyz.onion%2Fq%23%2F%3Fv%3D1%26dh%3Dk%26q%3Dc", true},
                {"modern lan link", "https://simplex.chat/contact#/?v=1&smp=smp%3A%2F%2Fabc%40192.168.1.10%3A5223%2Fq%23%2F%3Fv%3D1", true},
                {"legacy link", "https://smp.example.com/AbCdEf12345#ZyXwVu98765", true},
                {"empty", "", false},
                {"not https", "simplex:/contact#/?v=1&smp=x", false},
                {"trailing space (trimmed)", "https://simplex.chat/contact#/?v=1&smp=x ", true},
                {"internal whitespace", "https://simplex.chat/contact#/?v=1&smp=x y", false},
                {"no params", "https://simplex.chat/contact#/?", false},
                {"no fragment at all", "https://simplex.chat/contact", false},
                {"garbage", "hello world", false},
        }
        for _, c := range cases {
                err := ValidateRelayAddress(c.addr)
                if (err == nil) != c.ok {
                        t.Errorf("%s: ValidateRelayAddress(%q) err=%v, want ok=%v", c.name, c.addr, err, c.ok)
                }
        }
}

func TestRelayPlanOnionOnly(t *testing.T) {
        // the direct-address mode was removed for the final v1.5.4: the relay
        // registration path is the SMP onion through Tor (or a public host).
        plan := relayPlanSync(Settings{}, "smp://fp@abc.onion", 9050)
        if plan.Register != "smp://fp@abc.onion" || plan.Socks != 9050 {
                t.Errorf("onion plan = %+v", plan)
        }
        // public-host registration carries no SOCKS port
        plan = relayPlanSync(Settings{}, "smp://fp@example.com", 9050)
        if plan.Register != "smp://fp@example.com" || plan.Socks != 0 {
                t.Errorf("public plan = %+v", plan)
        }
        // a zero Settings still registers on the own SMP server
        // (independence from official SimpleX servers); only a missing
        // SMP address yields no registration target.
        plan = relayPlanSync(Settings{}, "smp://fp@abc.onion", 9050)
        if plan.Register != "smp://fp@abc.onion" || plan.Socks != 9050 {
                t.Errorf("zero-settings plan = %+v, want own-onion registration", plan)
        }
        plan = relayPlanSync(Settings{}, "", 9050)
        if plan != (RelayPlan{}) {
                t.Errorf("no-address plan = %+v, want zero", plan)
        }
}

func TestAddressOnionOnly(t *testing.T) {
        rec := &ServerRec{Fingerprint: "fp", OnionHost: "abc.onion", Port: 5223}
        got := Address(KindSMP, Settings{}, rec)
        if got != "smp://fp@abc.onion" {
                t.Errorf("SMP address = %q, want smp://fp@abc.onion", got)
        }
        // public host comes first, onion last
        got = Address(KindSMP, Settings{PublicHost: "example.com"}, rec)
        if got != "smp://fp@example.com,abc.onion" {
                t.Errorf("SMP address with public host = %q", got)
        }
}

func TestClientSocksPortOf(t *testing.T) {
        eng, _, st := newTestEngine(t)
        _ = eng
        if err := st.With(func(s *State) {
                s.Tor.SocksPort = 9050
                s.Tor.ClientSocksPort = 9060
        }); err != nil {
                t.Fatal(err)
        }
        s := st.Settings()
        s.SingleHopOnion = false
        _ = st.SetSettings(s)
        if got := ClientSocksPortOf(st); got != 9050 {
                t.Errorf("3-hop client socks = %d, want 9050", got)
        }
        s.SingleHopOnion = true
        _ = st.SetSettings(s)
        if got := ClientSocksPortOf(st); got != 9060 {
                t.Errorf("single-hop client socks = %d, want 9060", got)
        }
}
