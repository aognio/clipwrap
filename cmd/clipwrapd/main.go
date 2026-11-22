package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("clipwrapd: clipboard capture and distribution daemon")
		fmt.Println("Usage: clipwrapd [flags]")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --config PATH            Path to configuration file")
		fmt.Println("  --set-admin-password     Set or change the administrator password")
		fmt.Println("  --check-config           Validate configuration and exit")
		fmt.Println("  --version                Print version and exit")
		fmt.Println("  --help                   Print this help message")
		os.Exit(0)
	}

	fmt.Fprintf(os.Stderr, "clipwrapd: not yet implemented\n")
	os.Exit(1)
}
