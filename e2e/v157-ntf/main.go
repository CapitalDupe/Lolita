// v1.5.7 E2E: enable the notification server end to end in an isolated
// data root - the private PostgreSQL instance, the ntf-server binary
// download, init, the INI profile (db_connection + subscriber SOCKS
// proxy), the onion service and a RUNNING server bound to its port.
//
// Environment (set by the driving shell):
//
//	XDG_DATA_HOME       isolated data root
//	PATH                includes the PostgreSQL bin dir and tor
//	LD_LIBRARY_PATH     shared libs of the extracted prefixes
//
// Run: go run ./e2e/v157-ntf
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"sxops/internal/core"
)

type printLog struct{}

func (printLog) Step(msg string)   { fmt.Println("== " + msg) }
func (printLog) Detail(msg string) { fmt.Println("   " + msg) }

func main() {
	eng, err := core.NewEngine(printLog{})
	if err != nil {
		panic(err)
	}

	// preflight: the environment must have PostgreSQL available
	if _, err := core.FindPostgresBinDir(); err != nil {
		fmt.Println("PREFLIGHT FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("== preflight OK (PostgreSQL found)")

	fmt.Println("== enabling the notification server")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := eng.EnableNTFServer(ctx); err != nil {
		fmt.Println("ENABLE FAIL:", err)
		os.Exit(1)
	}

	// assertions
	rec := eng.State.Server(core.KindNTF)
	st := eng.Sv.Status(core.KindNTF)
	fmt.Printf("== NTF state: %s pid=%d installed=%v onion=%q\n", st.State, st.PID, rec.Installed, rec.OnionHost)
	if st.State != core.ProcRunning {
		fmt.Println("FAIL: NTF server is not running")
		dumpLog(eng)
		os.Exit(1)
	}
	addr := core.Address(core.KindNTF, eng.State.Settings(), rec)
	fmt.Println("== NTF address:", addr)
	if addr == "" || rec.Fingerprint == "" {
		fmt.Println("FAIL: no NTF address")
		os.Exit(1)
	}
	for _, st := range eng.Statuses() {
		fmt.Printf("   %-12s %s\n", st.Name+":", st.StateText)
	}

	fmt.Println("== re-run enable (idempotency)")
	if err := eng.EnableNTFServer(context.Background()); err != nil {
		fmt.Println("RE-ENABLE FAIL:", err)
		os.Exit(1)
	}
	if eng.Sv.Status(core.KindNTF).State != core.ProcRunning {
		fmt.Println("FAIL: NTF server not running after re-enable")
		os.Exit(1)
	}

	fmt.Println("== disable")
	if err := eng.DisableNTFServer(); err != nil {
		fmt.Println("DISABLE FAIL:", err)
		os.Exit(1)
	}
	time.Sleep(2 * time.Second)
	if eng.Sv.Status(core.KindNTF).State == core.ProcRunning {
		fmt.Println("FAIL: NTF server still running after disable")
		os.Exit(1)
	}
	fmt.Println("== re-enable again (data kept)")
	if err := eng.EnableNTFServer(context.Background()); err != nil {
		fmt.Println("REENABLE FAIL:", err)
		os.Exit(1)
	}
	if eng.Sv.Status(core.KindNTF).State != core.ProcRunning {
		fmt.Println("FAIL: NTF server not running after re-enable")
		dumpLog(eng)
		os.Exit(1)
	}

	fmt.Println("ALL NTF E2E CHECKS PASSED")
}

func dumpLog(eng *core.Engine) {
	if data, err := eng.Backend.ServerLogTail(core.KindNTF, 25); err == nil {
		fmt.Println("--- NTF log tail ---")
		fmt.Println(string(data))
	}
}
