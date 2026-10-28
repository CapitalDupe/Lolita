package main

import (
	"fmt"
	"os"

	"sxops/internal/core"
)

func main() {
	eng, err := core.NewEngine(core.NopInstallLog())
	if err != nil {
		panic(err)
	}
	fmt.Println("== SMP test on the fresh onion")
	if r := eng.RunServerTest(core.KindSMP); !r.Passed {
		fmt.Println("SMP FAIL:", r.Summary())
		os.Exit(1)
	}
	fmt.Println("SMP PASS")
	fmt.Println("== relay test")
	if r := eng.RunServerTest(core.KindRelay); !r.Passed {
		fmt.Println("RELAY FAIL:", r.Summary())
		os.Exit(1)
	}
	fmt.Println("RELAY PASS")
}
