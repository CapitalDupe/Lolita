package core

import (
        "net"
        "strconv"
        "strings"
        "testing"
        "time"
)

func TestOnionHostPort(t *testing.T) {
        cases := []struct {
                in    string
                host  string
                port  int
                found bool
        }{
                {"smp://AbCdEf=@abcdefgh...xyz.onion", "abcdefgh...xyz.onion", 5223, true},
                {"smp://AbCdEf=@abcdefgh.onion:5223", "abcdefgh.onion", 5223, true},
                {"smp://AbCdEf=@host.example.com:5223", "", 0, false},
                {"xftp://AbCdEf=@xyz.onion:5225", "xyz.onion", 5225, true},
        }
        for _, c := range cases {
                h, p, ok := onionHostPort(c.in)
                if ok != c.found || h != c.host || p != c.port {
                        t.Errorf("onionHostPort(%q) = (%q,%d,%v), want (%q,%d,%v)", c.in, h, p, ok, c.host, c.port, c.found)
                }
        }
}

// TestProbeTorSOCKS exercises the SOCKS5 client against a fake
// "Tor SOCKS" listener that accepts and then rejects connections - the
// same wire protocol probeTorSOCKS speaks to a real Tor.
func TestProbeTorSOCKS(t *testing.T) {
        // fake server: accept one connection with a successful CONNECT
        // reply, then one with a general failure.
        ln, err := net.Listen("tcp", "127.0.0.1:0")
        if err != nil {
                t.Fatal(err)
        }
        defer ln.Close()
        _, port, _ := net.SplitHostPort(ln.Addr().String())
        p, _ := strconv.Atoi(port)
        go func() {
                for i := 0; i < 2; i++ {
                        c, err := ln.Accept()
                        if err != nil {
                                return
                        }
                        buf := make([]byte, 64)
                        c.SetReadDeadline(time.Now().Add(2 * time.Second))
                        n, _ := c.Read(buf)
                        if i == 0 {
                                // greeting 05 01 00 -> reply 05 00
                                // connect req -> reply 05 00 00 01 127.0.0.1 1080
                                if n >= 3 && buf[0] == 0x05 {
                                        c.Write([]byte{0x05, 0x00})
                                }
                                c.SetReadDeadline(time.Now().Add(2 * time.Second))
                                if _, err := c.Read(buf); err == nil {
                                        c.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x04, 0x38})
                                }
                        } else {
                                // fail the CONNECT with REP=0x01
                                if n >= 3 && buf[0] == 0x05 {
                                        c.Write([]byte{0x05, 0x00})
                                }
                                c.SetReadDeadline(time.Now().Add(2 * time.Second))
                                if _, err := c.Read(buf); err == nil {
                                        c.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
                                }
                        }
                        c.Close()
                }
        }()
        if err := probeTorSOCKS(p, "aaaaaaaa.onion", 5223); err != nil {
                t.Errorf("expected success, got %v", err)
        }
        if err := probeTorSOCKS(p, "aaaaaaaa.onion", 5223); err == nil {
                t.Error("expected failure for rejected CONNECT")
        }
        // unreachable port
        if err := probeTorSOCKS(1, "aaaaaaaa.onion", 5223); err == nil {
                t.Error("expected failure for unreachable SOCKS port")
        }
}

func TestRelayPlanSync(t *testing.T) {
        s := Settings{}
        if p := relayPlanSync(s, "smp://fp@abc.onion", 9050); p.Socks != 9050 || p.Register != "smp://fp@abc.onion" {
                t.Errorf("onion address should register via Tor: %+v", p)
        }
        if p := relayPlanSync(s, "smp://fp@host.example.com", 9050); p.Socks != 0 || p.Register != "smp://fp@host.example.com" {
                t.Errorf("public host should register without SOCKS: %+v", p)
        }
        // The registration argv must always route onion servers through the
        // Tor SOCKS port (there is no direct/no-Tor fallback).
        argv := relayArgsWith(Settings{}, &Paths{}, relayPlanSync(Settings{}, "smp://fp@abc.onion", 9055))
        joined := strings.Join(argv, " ")
        if !strings.Contains(joined, "--relay-address-server smp://fp@abc.onion") || !strings.Contains(joined, "--socks-proxy 127.0.0.1:9055") {
                t.Errorf("relay argv must register via the onion through Tor SOCKS: %v", argv)
        }
}
