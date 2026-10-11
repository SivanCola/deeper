package main

import (
	"flag"
	"fmt"
	"os"

	"deepzora/desktop/internal/upgradefixture"
)

func main() {
	mode := flag.String("mode", "", "create or verify")
	home := flag.String("home", "", "isolated Deepzora home")
	report := flag.String("report", "", "fixture report path")
	phase := flag.String("phase", "", "verification phase")
	flag.Parse()
	if err := upgradefixture.Run(*mode, *home, *report, *phase); err != nil {
		fmt.Fprintln(os.Stderr, "windows-upgrade-fixture:", err)
		os.Exit(1)
	}
}
