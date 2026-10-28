package core

import "testing"

// v1.5.9: the chat relay registers its link ONLY on the own SMP server -
// there is no official-server fallback anywhere.
func TestRelayPlanAlwaysOwnServer(t *testing.T) {
        s := Settings{}
        if p := relayPlanSync(s, "smp://fp@abc.onion", 9050); p.Register != "smp://fp@abc.onion" || p.Socks != 9050 {
                t.Errorf("the relay must register via the own onion: %+v", p)
        }
        if p := relayPlanSync(s, "smp://fp@host.example.com", 9050); p.Register != "smp://fp@host.example.com" || p.Socks != 0 {
                t.Errorf("a public own host must register without SOCKS: %+v", p)
        }
        if p := relayPlanSync(s, "", 9050); p.Register != "" {
                t.Errorf("no own SMP address -> no registration target: %+v", p)
        }
}

func TestRelayLinkHost(t *testing.T) {
        for _, c := range []struct{ link, want string }{
                {"https://smp6.simplex.im/r#KEY", "smp6.simplex.im"},
                {"https://abc123.onion/r#KEY?p=5223&c=fp", "abc123.onion"},
                {"https://host.example.com:8443/r#KEY", "host.example.com"},
                {"http://x.onion/#frag", "x.onion"},
                {"https://nohost", "nohost"},
                {"not-a-link", "not-a-link"},
                {"", ""},
        } {
                if got := RelayLinkHost(c.link); got != c.want {
                        t.Errorf("RelayLinkHost(%q) = %q, want %q", c.link, got, c.want)
                }
        }
}

func TestRelayLinkForeign(t *testing.T) {
        own := "smp://FP@pub.example.com,abc123.onion"
        if _, f := RelayLinkForeign(own, "https://abc123.onion/r#k?p=5223&c=x"); f {
                t.Error("a link on the own onion host must not be foreign")
        }
        if _, f := RelayLinkForeign(own, "https://pub.example.com/r#k"); f {
                t.Error("a link on the own public host must not be foreign")
        }
        if h, f := RelayLinkForeign(own, "https://smp6.simplex.im/r#k"); !f || h != "smp6.simplex.im" {
                t.Errorf("an official-server link must be foreign (got %q, %v)", h, f)
        }
        if _, f := RelayLinkForeign(own, ""); f {
                t.Error("an empty link is not foreign")
        }
        if _, f := RelayLinkForeign(own, "not-a-link"); f {
                t.Error("a non-http value is not foreign")
        }
        if _, f := RelayLinkForeign("", "https://abc123.onion/r#k"); f {
                t.Error("no own SMP address -> not judged foreign (no half-configured migrations)")
        }
        if _, f := RelayLinkForeign("smp://fp@YOUR-SERVER-HOST", "https://abc123.onion/r#k"); f {
                t.Error("a placeholder own address -> not judged foreign")
        }
}
