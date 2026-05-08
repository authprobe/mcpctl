package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	configFileMode = 0o644
	exitOK         = 0
	exitUsage      = 2
	exitError      = 1
)

// Runner owns command dispatch and output streams for the mcpctl CLI.
//
// Args:
//
//	stdout: Writer used for normal command output.
//	stderr: Writer used for diagnostics and usage errors.
//
// Returns:
//
//	A CLI runner with no ambient global output dependencies.
type Runner struct {
	stdout io.Writer
	stderr io.Writer
}

// New creates a CLI runner with explicit output streams for tests and callers.
//
// Args:
//
//	stdout: Writer that receives command output; nil falls back to io.Discard.
//	stderr: Writer that receives diagnostics; nil falls back to io.Discard.
//
// Returns:
//
//	A configured Runner ready to execute command arguments.
func New(stdout io.Writer, stderr io.Writer) *Runner {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return &Runner{stdout: stdout, stderr: stderr}
}

// Run dispatches parsed command arguments to the matching mcpctl command.
//
// Args:
//
//	args: Command line arguments after the executable name.
//
// Returns:
//
//	Process-style exit code: 0 for success, 1 for runtime errors, 2 for usage errors.
func (r *Runner) Run(args []string) int {
	if len(args) == 0 || isHelp(args[0]) {
		r.writeHelp(r.stdout)
		return exitOK
	}

	switch args[0] {
	case "init":
		return r.runInit(args[1:])
	case "dev", "inspect", "validate", "report", "doctor":
		return r.runLocalPlaceholder(args[0], args[1:])
	case "registry":
		return r.runRegistry(args[1:])
	case "version":
		return r.runVersion(args[1:])
	default:
		fmt.Fprintf(r.stderr, "unknown command %q\n\n", args[0])
		r.writeHelp(r.stderr)
		return exitUsage
	}
}

// runInit writes the starter mcpctl.yaml config without overwriting by default.
//
// Args:
//
//	args: Flags for init; supports -config and -force.
//
// Returns:
//
//	Process-style exit code indicating whether the config was created.
//
// Errors:
//
//	Returns a non-zero code when flags are invalid or the config cannot be written.
func (r *Runner) runInit(args []string) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(r.stderr)
	configPath := flags.String("config", "mcpctl.yaml", "path to write the mcpctl config")
	force := flags.Bool("force", false, "overwrite an existing config")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(r.stderr, "init does not accept positional arguments: %s\n", strings.Join(flags.Args(), " "))
		return exitUsage
	}

	if err := writeInitialConfig(*configPath, *force); err != nil {
		if errors.Is(err, os.ErrExist) {
			fmt.Fprintf(r.stderr, "%s already exists; rerun with -force to overwrite\n", *configPath)
			return exitError
		}
		fmt.Fprintf(r.stderr, "failed to write %s: %v\n", *configPath, err)
		return exitError
	}

	fmt.Fprintf(r.stdout, "created %s\n", *configPath)
	return exitOK
}

// runLocalPlaceholder exposes planned local commands while implementation expands.
//
// Args:
//
//	name: Command name selected by the user.
//	args: Remaining flags and positional arguments.
//
// Returns:
//
//	Process-style exit code; help succeeds and normal execution reports incomplete support.
func (r *Runner) runLocalPlaceholder(name string, args []string) int {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Fprintf(r.stdout, "%s is available as a command surface but is not implemented yet.\n", name)
		return exitOK
	}
	fmt.Fprintf(r.stderr, "%s is not implemented yet\n", name)
	return exitError
}

// runRegistry dispatches registry subcommands such as metadata export.
//
// Args:
//
//	args: Registry subcommand arguments; currently supports export as a placeholder.
//
// Returns:
//
//	Process-style exit code for registry command handling.
func (r *Runner) runRegistry(args []string) int {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(r.stdout, "Usage: mcpctl registry export [flags]")
		return exitOK
	}
	if args[0] != "export" {
		fmt.Fprintf(r.stderr, "unknown registry command %q\n", args[0])
		return exitUsage
	}
	return r.runLocalPlaceholder("registry export", args[1:])
}

// runVersion prints the current development version of the CLI.
//
// Args:
//
//	args: Optional help flag; any other argument is a usage error.
//
// Returns:
//
//	Process-style exit code for version output.
func (r *Runner) runVersion(args []string) int {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Fprintln(r.stdout, "Usage: mcpctl version")
		return exitOK
	}
	if len(args) > 0 {
		fmt.Fprintf(r.stderr, "version does not accept arguments: %s\n", strings.Join(args, " "))
		return exitUsage
	}
	fmt.Fprintln(r.stdout, "mcpctl dev")
	return exitOK
}

// writeHelp prints concise top-level usage for the public CLI surface.
//
// Args:
//
//	out: Destination for usage text.
//
// Returns:
//
//	None. The function writes directly to the supplied writer.
func (r *Runner) writeHelp(out io.Writer) {
	commands := []string{"init", "dev", "inspect", "validate", "report", "registry export", "doctor", "version"}
	sort.Strings(commands)

	fmt.Fprintln(out, "Usage: mcpctl <command> [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	for _, command := range commands {
		fmt.Fprintf(out, "  %s\n", command)
	}
}

// isHelp reports whether an argument requests usage output.
//
// Args:
//
//	arg: A single command line argument to classify.
//
// Returns:
//
//	True when the argument is -h, --help, or help.
func isHelp(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

// writeInitialConfig creates the default config file used by early local commands.
//
// Args:
//
//	path: File path for the generated config; parent directories are created when needed.
//	force: Whether an existing file may be overwritten.
//
// Returns:
//
//	nil on success, os.ErrExist when overwrite is refused, or an I/O error.
func writeInitialConfig(path string, force bool) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path cannot be empty")
	}

	if !force {
		if _, err := os.Stat(path); err == nil {
			return os.ErrExist
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	return os.WriteFile(path, []byte(defaultConfig()), configFileMode)
}

// defaultConfig returns the minimal starter configuration for local MCP workflows.
//
// Args:
//
//	None.
//
// Returns:
//
//	YAML text with conservative defaults and placeholders for local execution.
func defaultConfig() string {
	return strings.TrimLeft(`
version: 1
server:
  command: ""
  args: []
transport:
  type: stdio
`, "\n")
}
