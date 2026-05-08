package main

import (
	"os"

	"github.com/authprobe/mcpctl/internal/cli"
)

// main runs the mcpctl command line entrypoint and exits with its status code.
//
// Args:
//
//	None. Command arguments are read from os.Args.
//
// Returns:
//
//	None. The process exits with the status returned by the CLI runner.
func main() {
	os.Exit(cli.New(os.Stdout, os.Stderr).Run(os.Args[1:]))
}
