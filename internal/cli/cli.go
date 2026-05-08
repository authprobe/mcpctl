package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	configFileMode = 0o644
	exitOK         = 0
	exitUsage      = 2
	exitError      = 1
	defaultCloud   = "https://mcpctl.io"
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
	stdout     io.Writer
	stderr     io.Writer
	httpClient *http.Client
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
	return NewWithHTTPClient(stdout, stderr, http.DefaultClient)
}

// NewWithHTTPClient creates a CLI runner with explicit streams and cloud transport.
//
// Args:
//
//	stdout: Writer that receives command output; nil falls back to io.Discard.
//	stderr: Writer that receives diagnostics; nil falls back to io.Discard.
//	httpClient: Client used only for explicit cloud commands; nil uses http.DefaultClient.
//
// Returns:
//
//	A configured Runner ready to execute command arguments.
func NewWithHTTPClient(stdout io.Writer, stderr io.Writer, httpClient *http.Client) *Runner {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Runner{stdout: stdout, stderr: stderr, httpClient: httpClient}
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
	case "auth":
		return r.runAuth(args[1:])
	case "cloud":
		return r.runCloud(args[1:])
	case "login":
		return r.runAuth(append([]string{"login"}, args[1:]...))
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
		r.writeCommandHelp(r.stdout, name)
		return exitOK
	}
	if name == "dev" || name == "inspect" || name == "validate" {
		if _, err := os.Stat("mcpctl.yaml"); errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(r.stderr, "%s needs mcpctl.yaml; run `mcpctl init` first or pass a config path when that flag lands.\n", name)
			return exitError
		}
	}
	fmt.Fprintf(r.stderr, "%s is not implemented yet\n", name)
	return exitError
}

// runAuth dispatches cloud authentication command surfaces.
//
// Args:
//
//	args: Auth subcommand arguments after "auth".
//
// Returns:
//
//	Process-style exit code for auth command handling.
func (r *Runner) runAuth(args []string) int {
	if len(args) == 0 || isHelp(args[0]) {
		r.writeAuthHelp(r.stdout)
		return exitOK
	}

	switch args[0] {
	case "login":
		return r.runAuthLogin(args[1:])
	case "status":
		return r.runAuthStatus(args[1:])
	case "logout":
		return r.runAuthLogout(args[1:])
	default:
		fmt.Fprintf(r.stderr, "unknown auth command %q\n", args[0])
		return exitUsage
	}
}

// runAuthLogin exposes the browser-login UX before the live cloud endpoint is wired.
//
// Args:
//
//	args: Auth login flags; supports help and --with-token.
//
// Returns:
//
//	Process-style exit code; help succeeds and live login reports pending cloud support.
func (r *Runner) runAuthLogin(args []string) int {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Fprintln(r.stdout, "Usage: mcpctl auth login [--web] [--with-token]")
		fmt.Fprintf(r.stdout, "Start browser-based login for %s. Local commands do not require login.\n", defaultCloud)
		return exitOK
	}
	flags := flag.NewFlagSet("auth login", flag.ContinueOnError)
	flags.SetOutput(r.stderr)
	withToken := flags.Bool("with-token", false, "read a token from standard input for automation")
	web := flags.Bool("web", true, "open browser-based login when available")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(r.stderr, "auth login does not accept positional arguments: %s\n", strings.Join(flags.Args(), " "))
		return exitUsage
	}
	if *withToken {
		fmt.Fprintln(r.stderr, "token input is not implemented yet; use MCPCTL_TOKEN with future managed commands")
		return exitError
	}
	if !*web {
		fmt.Fprintln(r.stderr, "non-browser interactive login is not implemented yet")
		return exitError
	}
	fmt.Fprintf(r.stderr, "browser login for %s is not implemented yet\n", defaultCloud)
	return exitError
}

// runAuthStatus reports whether local cloud credentials are available.
//
// Args:
//
//	args: Auth status flags; no flags are currently accepted.
//
// Returns:
//
//	Process-style exit code for credential status reporting.
func (r *Runner) runAuthStatus(args []string) int {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Fprintln(r.stdout, "Usage: mcpctl auth status")
		return exitOK
	}
	if len(args) > 0 {
		fmt.Fprintf(r.stderr, "auth status does not accept arguments: %s\n", strings.Join(args, " "))
		return exitUsage
	}
	if os.Getenv("MCPCTL_TOKEN") != "" {
		fmt.Fprintln(r.stdout, "mcpctl.io: authenticated via MCPCTL_TOKEN")
		return exitOK
	}
	fmt.Fprintln(r.stdout, "mcpctl.io: not authenticated")
	fmt.Fprintln(r.stdout, "Run `mcpctl auth login` when you need hosted workflows.")
	return exitOK
}

// runAuthLogout exposes credential removal before persisted credentials are available.
//
// Args:
//
//	args: Auth logout flags; no flags are currently accepted.
//
// Returns:
//
//	Process-style exit code for logout handling.
func (r *Runner) runAuthLogout(args []string) int {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Fprintln(r.stdout, "Usage: mcpctl auth logout")
		return exitOK
	}
	if len(args) > 0 {
		fmt.Fprintf(r.stderr, "auth logout does not accept arguments: %s\n", strings.Join(args, " "))
		return exitUsage
	}
	fmt.Fprintln(r.stdout, "No stored mcpctl.io credentials found.")
	return exitOK
}

// runCloud dispatches no-login cloud utility commands.
//
// Args:
//
//	args: Cloud subcommand arguments after "cloud".
//
// Returns:
//
//	Process-style exit code for cloud command handling.
func (r *Runner) runCloud(args []string) int {
	if len(args) == 0 || isHelp(args[0]) {
		r.writeCloudHelp(r.stdout)
		return exitOK
	}
	if args[0] != "ping" {
		fmt.Fprintf(r.stderr, "unknown cloud command %q\n", args[0])
		return exitUsage
	}
	return r.runCloudPing(args[1:])
}

// runCloudPing checks mcpctl.io reachability without requiring authentication.
//
// Args:
//
//	args: Flags for cloud ping; supports -endpoint and -timeout.
//
// Returns:
//
//	Process-style exit code indicating whether the cloud endpoint is reachable.
//
// Errors:
//
//	Returns a non-zero code when the endpoint URL is invalid or unreachable.
func (r *Runner) runCloudPing(args []string) int {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Fprintln(r.stdout, "Usage: mcpctl cloud ping [-endpoint URL] [-timeout duration]")
		fmt.Fprintln(r.stdout, "Check mcpctl.io reachability without logging in.")
		return exitOK
	}
	flags := flag.NewFlagSet("cloud ping", flag.ContinueOnError)
	flags.SetOutput(r.stderr)
	endpoint := flags.String("endpoint", defaultCloud, "cloud endpoint to check")
	timeout := flags.Duration("timeout", 5*time.Second, "maximum time to wait")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(r.stderr, "cloud ping does not accept positional arguments: %s\n", strings.Join(flags.Args(), " "))
		return exitUsage
	}
	parsed, err := url.ParseRequestURI(*endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		fmt.Fprintf(r.stderr, "invalid cloud endpoint %q\n", *endpoint)
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *endpoint, nil)
	if err != nil {
		fmt.Fprintf(r.stderr, "failed to create cloud request: %v\n", err)
		return exitError
	}
	req.Header.Set("User-Agent", "mcpctl/dev")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		fmt.Fprintf(r.stderr, "mcpctl.io is unreachable: %v\n", err)
		return exitError
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError {
		fmt.Fprintf(r.stderr, "mcpctl.io returned %s\n", resp.Status)
		return exitError
	}
	fmt.Fprintf(r.stdout, "mcpctl.io reachable: %s\n", resp.Status)
	return exitOK
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
	commands := []string{"init", "dev", "inspect", "validate", "auth login", "auth status", "cloud ping", "report", "registry export", "doctor", "version"}
	sort.Strings(commands)

	fmt.Fprintln(out, "Usage: mcpctl <command> [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	for _, command := range commands {
		fmt.Fprintf(out, "  %s\n", command)
	}
}

// writeCommandHelp prints usage for local developer-readiness commands.
//
// Args:
//
//	out: Destination for usage text.
//	name: Command name to describe.
//
// Returns:
//
//	None. The function writes directly to the supplied writer.
func (r *Runner) writeCommandHelp(out io.Writer, name string) {
	switch name {
	case "dev":
		fmt.Fprintln(out, "Usage: mcpctl dev [flags]")
		fmt.Fprintln(out, "Run the MCP server from mcpctl.yaml and report local readiness.")
	case "inspect":
		fmt.Fprintln(out, "Usage: mcpctl inspect [flags]")
		fmt.Fprintln(out, "Discover tools, resources, prompts, schemas, and transport metadata.")
	case "validate":
		fmt.Fprintln(out, "Usage: mcpctl validate [flags]")
		fmt.Fprintln(out, "Check whether MCP tools are described clearly enough for agents.")
	default:
		fmt.Fprintf(out, "%s is available as a command surface but is not implemented yet.\n", name)
	}
}

// writeAuthHelp prints usage for mcpctl.io authentication commands.
//
// Args:
//
//	out: Destination for usage text.
//
// Returns:
//
//	None. The function writes directly to the supplied writer.
func (r *Runner) writeAuthHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage: mcpctl auth <command> [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  login   Start browser-based login for mcpctl.io")
	fmt.Fprintln(out, "  status  Show local cloud credential status")
	fmt.Fprintln(out, "  logout  Remove local cloud credentials")
}

// writeCloudHelp prints usage for no-login mcpctl.io utility commands.
//
// Args:
//
//	out: Destination for usage text.
//
// Returns:
//
//	None. The function writes directly to the supplied writer.
func (r *Runner) writeCloudHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage: mcpctl cloud <command> [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  ping  Check mcpctl.io reachability without logging in")
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
