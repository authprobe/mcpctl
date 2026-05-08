package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	configFileMode  = 0o644
	exitOK          = 0
	exitUsage       = 2
	exitError       = 1
	defaultCloud    = "https://mcpctl.io"
	keychainService = "mcpctl"
	keychainAccount = "mcpctl.io"
)

var errCredentialNotFound = errors.New("credential not found")

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
	stdin      io.Reader
	httpClient *http.Client
	store      credentialStore
	openURL    func(string) error
	sleep      func(time.Duration)
}

// credentialStore persists cloud credentials without exposing token values in output.
//
// Args:
//
//	None. Implementations receive values through their methods.
//
// Returns:
//
//	Implementations return stored credential data or errors from their backing store.
type credentialStore interface {
	Save(credentialRecord) error
	Load() (credentialRecord, error)
	Delete() error
}

// credentialRecord stores mcpctl.io authentication material and display metadata.
//
// Args:
//
//	None. Values are populated from cloud token responses.
//
// Returns:
//
//	The struct is serialized as one credential-store payload.
type credentialRecord struct {
	Host         string    `json:"host"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	AccountLogin string    `json:"account_login,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
	Source       string    `json:"source"`
}

// deviceAuthorizationResponse captures the first response in browser login.
//
// Args:
//
//	None. Values are decoded from mcpctl.io JSON.
//
// Returns:
//
//	The struct contains the device code, user code, browser URL, expiry, and poll interval.
type deviceAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// tokenResponse captures token polling responses from mcpctl.io.
//
// Args:
//
//	None. Values are decoded from mcpctl.io JSON.
//
// Returns:
//
//	The struct carries either a pending/error status or approved credential data.
type tokenResponse struct {
	Status       string       `json:"status"`
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	TokenType    string       `json:"token_type"`
	ExpiresAt    string       `json:"expires_at"`
	Account      tokenAccount `json:"account"`
	Scopes       []string     `json:"scopes"`
	Interval     int          `json:"interval"`
}

// tokenAccount contains display metadata for the authenticated cloud account.
//
// Args:
//
//	None. Values are decoded from mcpctl.io JSON.
//
// Returns:
//
//	The struct contains non-secret identity fields safe for status output.
type tokenAccount struct {
	ID    string `json:"id"`
	Login string `json:"login"`
}

// fileCredentialStore stores credentials through the OS credential store when possible.
//
// Args:
//
//	None. The struct is configured through its fields at construction time.
//
// Returns:
//
//	Methods return persisted credentials or storage errors.
type fileCredentialStore struct {
	allowPlaintext bool
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
	return &Runner{
		stdout:     stdout,
		stderr:     stderr,
		stdin:      os.Stdin,
		httpClient: httpClient,
		store:      fileCredentialStore{},
		openURL:    openBrowser,
		sleep:      time.Sleep,
	}
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
		fmt.Fprintln(r.stdout, "Usage: mcpctl auth login [--web] [--with-token] [--insecure-storage] [-endpoint URL]")
		fmt.Fprintf(r.stdout, "Start browser-based login for %s. Local commands do not require login.\n", defaultCloud)
		return exitOK
	}
	flags := flag.NewFlagSet("auth login", flag.ContinueOnError)
	flags.SetOutput(r.stderr)
	endpoint := flags.String("endpoint", defaultCloud, "cloud endpoint for browser login")
	withToken := flags.Bool("with-token", false, "read a token from standard input for automation")
	web := flags.Bool("web", true, "open browser-based login when available")
	insecureStorage := flags.Bool("insecure-storage", false, "allow plaintext credential fallback when OS storage is unavailable")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(r.stderr, "auth login does not accept positional arguments: %s\n", strings.Join(flags.Args(), " "))
		return exitUsage
	}
	if *withToken {
		return r.runAuthLoginWithToken(*endpoint, *insecureStorage)
	}
	return r.runBrowserLogin(*endpoint, *web, *insecureStorage)
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
	credential, err := r.store.Load()
	if err == nil {
		login := credential.AccountLogin
		if login == "" {
			login = "unknown account"
		}
		fmt.Fprintf(r.stdout, "%s: authenticated as %s via %s\n", credential.Host, login, credential.Source)
		if len(credential.Scopes) > 0 {
			fmt.Fprintf(r.stdout, "scopes: %s\n", strings.Join(credential.Scopes, ","))
		}
		if !credential.ExpiresAt.IsZero() {
			fmt.Fprintf(r.stdout, "access token expires: %s\n", credential.ExpiresAt.Format(time.RFC3339))
		}
		return exitOK
	}
	if !errors.Is(err, errCredentialNotFound) {
		fmt.Fprintf(r.stderr, "failed to read stored mcpctl.io credentials: %v\n", err)
		return exitError
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
		fmt.Fprintln(r.stdout, "Usage: mcpctl auth logout [-endpoint URL]")
		return exitOK
	}
	flags := flag.NewFlagSet("auth logout", flag.ContinueOnError)
	flags.SetOutput(r.stderr)
	endpoint := flags.String("endpoint", defaultCloud, "cloud endpoint for token revocation")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(r.stderr, "auth logout does not accept arguments: %s\n", strings.Join(flags.Args(), " "))
		return exitUsage
	}
	credential, err := r.store.Load()
	if errors.Is(err, errCredentialNotFound) {
		fmt.Fprintln(r.stdout, "No stored mcpctl.io credentials found.")
		return exitOK
	}
	if err != nil {
		fmt.Fprintf(r.stderr, "failed to read stored mcpctl.io credentials: %v\n", err)
		return exitError
	}
	if credential.RefreshToken != "" {
		if err := r.revokeToken(*endpoint, credential.RefreshToken); err != nil {
			fmt.Fprintf(r.stderr, "warning: cloud token revocation failed: %v\n", err)
		}
	}
	if err := r.store.Delete(); err != nil && !errors.Is(err, errCredentialNotFound) {
		fmt.Fprintf(r.stderr, "failed to remove stored mcpctl.io credentials: %v\n", err)
		return exitError
	}
	fmt.Fprintln(r.stdout, "Logged out of mcpctl.io.")
	return exitOK
}

// runAuthLoginWithToken stores an explicitly provided token for automation workflows.
//
// Args:
//
//	endpoint: Cloud endpoint associated with the token.
//	allowPlaintext: Whether plaintext file fallback is allowed when OS storage is unavailable.
//
// Returns:
//
//	Process-style exit code for token ingestion and storage.
//
// Errors:
//
//	Returns a non-zero code when no token is provided or credential storage fails.
func (r *Runner) runAuthLoginWithToken(endpoint string, allowPlaintext bool) int {
	tokenBytes, err := io.ReadAll(r.stdin)
	if err != nil {
		fmt.Fprintf(r.stderr, "failed to read token from stdin: %v\n", err)
		return exitError
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		fmt.Fprintln(r.stderr, "no token provided on stdin")
		return exitError
	}
	store := r.credentialStore(allowPlaintext)
	if err := store.Save(credentialRecord{
		Host:        endpoint,
		AccessToken: token,
		TokenType:   "bearer",
		Source:      "stdin",
	}); err != nil {
		fmt.Fprintf(r.stderr, "failed to store token: %v\n", err)
		return exitError
	}
	fmt.Fprintf(r.stdout, "Stored credentials for %s.\n", endpoint)
	return exitOK
}

// runBrowserLogin completes browser-based device authorization against mcpctl.io.
//
// Args:
//
//	endpoint: Cloud endpoint that serves the device authorization API.
//	openWeb: Whether the CLI should attempt to open the verification URL.
//	allowPlaintext: Whether plaintext file fallback is allowed when OS storage is unavailable.
//
// Returns:
//
//	Process-style exit code for the login flow.
//
// Errors:
//
//	Returns a non-zero code when device creation, approval polling, or credential storage fails.
func (r *Runner) runBrowserLogin(endpoint string, openWeb bool, allowPlaintext bool) int {
	device, err := r.createDeviceAuthorization(endpoint)
	if err != nil {
		fmt.Fprintf(r.stderr, "failed to start browser login: %v\n", err)
		return exitError
	}

	verificationURL := device.VerificationURIComplete
	if verificationURL == "" {
		verificationURL = device.VerificationURI
	}
	fmt.Fprintf(r.stdout, "Open this URL to authenticate: %s\n", verificationURL)
	fmt.Fprintf(r.stdout, "Enter code: %s\n", device.UserCode)
	if device.ExpiresIn > 0 {
		fmt.Fprintf(r.stdout, "Code expires in %d seconds.\n", device.ExpiresIn)
	}
	if openWeb && verificationURL != "" {
		if err := r.openURL(verificationURL); err != nil {
			fmt.Fprintf(r.stderr, "could not open browser automatically: %v\n", err)
			fmt.Fprintln(r.stderr, "Copy the URL and code above to finish login.")
		}
	}

	token, err := r.pollDeviceToken(endpoint, device)
	if err != nil {
		fmt.Fprintf(r.stderr, "browser login failed: %v\n", err)
		return exitError
	}
	credential, err := credentialFromToken(endpoint, token)
	if err != nil {
		fmt.Fprintf(r.stderr, "browser login returned invalid credentials: %v\n", err)
		return exitError
	}
	if err := r.credentialStore(allowPlaintext).Save(credential); err != nil {
		fmt.Fprintf(r.stderr, "failed to store mcpctl.io credentials: %v\n", err)
		return exitError
	}
	login := credential.AccountLogin
	if login == "" {
		login = "mcpctl.io"
	}
	fmt.Fprintf(r.stdout, "Logged in to %s as %s.\n", endpoint, login)
	return exitOK
}

// createDeviceAuthorization requests a one-time browser login code from mcpctl.io.
//
// Args:
//
//	endpoint: Base cloud endpoint URL.
//
// Returns:
//
//	Device authorization response with browser URL, user code, expiry, and poll interval.
//
// Errors:
//
//	Returns an error when the endpoint is invalid, unavailable, or returns malformed JSON.
func (r *Runner) createDeviceAuthorization(endpoint string) (deviceAuthorizationResponse, error) {
	payload := map[string]any{
		"client":           "mcpctl",
		"host":             endpoint,
		"requested_scopes": []string{"inspect:write", "compat:run", "reports:share", "account:read"},
	}
	var device deviceAuthorizationResponse
	if err := r.postJSON(endpoint, "/v1/cli/device-authorizations", payload, &device); err != nil {
		return deviceAuthorizationResponse{}, err
	}
	if device.DeviceCode == "" || device.UserCode == "" || (device.VerificationURI == "" && device.VerificationURIComplete == "") {
		return deviceAuthorizationResponse{}, fmt.Errorf("device authorization response missing required fields")
	}
	return device, nil
}

// pollDeviceToken waits for browser approval and returns approved token data.
//
// Args:
//
//	endpoint: Base cloud endpoint URL.
//	device: Device authorization response returned by createDeviceAuthorization.
//
// Returns:
//
//	Token response containing access token, refresh token, account, scopes, and expiry.
//
// Errors:
//
//	Returns an error when approval is denied, expires, or polling cannot complete.
func (r *Runner) pollDeviceToken(endpoint string, device deviceAuthorizationResponse) (tokenResponse, error) {
	interval := time.Duration(device.Interval) * time.Second
	if interval < 0 {
		interval = 0
	}
	deadline := time.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
	if device.ExpiresIn <= 0 {
		deadline = time.Now().Add(15 * time.Minute)
	}
	for {
		if time.Now().After(deadline) {
			return tokenResponse{}, fmt.Errorf("device code expired")
		}
		var token tokenResponse
		err := r.postJSON(endpoint, "/v1/cli/token", map[string]string{"device_code": device.DeviceCode}, &token)
		if err != nil {
			return tokenResponse{}, err
		}
		switch token.Status {
		case "approved":
			if token.AccessToken == "" {
				return tokenResponse{}, fmt.Errorf("approved token response missing access token")
			}
			if token.RefreshToken == "" {
				return tokenResponse{}, fmt.Errorf("approved token response missing refresh token")
			}
			return token, nil
		case "authorization_pending", "":
			r.sleep(interval)
		case "slow_down":
			if token.Interval > 0 {
				interval = time.Duration(token.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
			r.sleep(interval)
		case "access_denied":
			return tokenResponse{}, fmt.Errorf("access denied")
		case "expired_token":
			return tokenResponse{}, fmt.Errorf("device code expired")
		default:
			return tokenResponse{}, fmt.Errorf("unexpected token status %q", token.Status)
		}
	}
}

// revokeToken asks mcpctl.io to revoke a stored refresh token during logout.
//
// Args:
//
//	endpoint: Base cloud endpoint URL.
//	refreshToken: Refresh token to revoke; raw value is never printed.
//
// Returns:
//
//	nil when revocation succeeds or a descriptive HTTP/client error.
func (r *Runner) revokeToken(endpoint string, refreshToken string) error {
	return r.postJSON(endpoint, "/v1/cli/token/revoke", map[string]string{"refresh_token": refreshToken}, nil)
}

// postJSON sends a JSON POST request and decodes the JSON response.
//
// Args:
//
//	endpoint: Base cloud endpoint URL.
//	path: Absolute API path under the cloud endpoint.
//	payload: JSON-serializable request body.
//	out: Destination for response JSON.
//
// Returns:
//
//	nil when the request succeeds with a 2xx response and JSON decodes into out.
//
// Errors:
//
//	Returns endpoint, transport, status, or JSON decoding errors.
func (r *Runner) postJSON(endpoint string, path string, payload any, out any) error {
	requestURL, err := joinEndpointPath(endpoint, path)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "mcpctl/dev")
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s returned %s: %s", requestURL, resp.Status, strings.TrimSpace(string(limited)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	return nil
}

// credentialStore returns the active credential store for this command.
//
// Args:
//
//	allowPlaintext: Whether plaintext fallback storage may be used for this command.
//
// Returns:
//
//	A credential store configured with the command's storage policy.
func (r *Runner) credentialStore(allowPlaintext bool) credentialStore {
	if store, ok := r.store.(fileCredentialStore); ok {
		store.allowPlaintext = allowPlaintext
		return store
	}
	return r.store
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

// credentialFromToken normalizes a cloud token response into local credential data.
//
// Args:
//
//	host: Cloud endpoint associated with the credential.
//	token: Approved token response returned by mcpctl.io.
//
// Returns:
//
//	Credential record safe to persist in the configured credential store.
func credentialFromToken(host string, token tokenResponse) (credentialRecord, error) {
	expiresAt, err := parseExpiresAt(token.ExpiresAt)
	if err != nil {
		return credentialRecord{}, err
	}
	tokenType := token.TokenType
	if tokenType == "" {
		tokenType = "bearer"
	}
	return credentialRecord{
		Host:         host,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    tokenType,
		ExpiresAt:    expiresAt,
		AccountLogin: token.Account.Login,
		Scopes:       token.Scopes,
		Source:       "credential-store",
	}, nil
}

// parseExpiresAt parses optional token expiry timestamps from cloud responses.
//
// Args:
//
//	value: RFC3339 timestamp string; empty values mean no known expiry.
//
// Returns:
//
//	Parsed time or zero time when the input is empty.
//
// Errors:
//
//	Returns a parsing error when a non-empty timestamp is not RFC3339.
func parseExpiresAt(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

// joinEndpointPath joins a cloud endpoint and API path without losing base path data.
//
// Args:
//
//	endpoint: Base cloud endpoint URL.
//	path: Absolute API path under the endpoint.
//
// Returns:
//
//	Fully-qualified URL for the API request.
//
// Errors:
//
//	Returns an error when the endpoint is not an absolute HTTP(S) URL.
func joinEndpointPath(endpoint string, path string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid cloud endpoint %q", endpoint)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("cloud endpoint must use http or https")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

// openBrowser opens a URL in the user's default browser when the platform supports it.
//
// Args:
//
//	target: URL to open.
//
// Returns:
//
//	nil when the platform opener command starts successfully.
func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

// Save persists credentials using OS storage or an explicitly allowed plaintext fallback.
//
// Args:
//
//	credential: Cloud credential record containing access and refresh tokens.
//
// Returns:
//
//	nil when credentials are persisted.
//
// Errors:
//
//	Returns an error when OS storage fails and plaintext fallback is not allowed.
func (s fileCredentialStore) Save(credential credentialRecord) error {
	if runtime.GOOS == "darwin" {
		if err := saveMacOSKeychain(credential); err == nil {
			return nil
		} else if !s.allowPlaintext {
			return err
		}
	}
	if !s.allowPlaintext {
		return fmt.Errorf("OS credential store unavailable; rerun with --insecure-storage to allow plaintext fallback")
	}
	return savePlaintextCredential(credential)
}

// Load reads credentials from OS storage or an existing fallback file.
//
// Args:
//
//	None.
//
// Returns:
//
//	Stored cloud credential record.
//
// Errors:
//
//	Returns errCredentialNotFound when no stored credential exists.
func (s fileCredentialStore) Load() (credentialRecord, error) {
	if runtime.GOOS == "darwin" {
		credential, err := loadMacOSKeychain()
		if err == nil {
			return credential, nil
		}
	}
	return loadPlaintextCredential()
}

// Delete removes credentials from OS storage and fallback file storage.
//
// Args:
//
//	None.
//
// Returns:
//
//	nil when no credentials remain.
//
// Errors:
//
//	Returns unexpected OS or filesystem deletion errors.
func (s fileCredentialStore) Delete() error {
	var deleteErr error
	if runtime.GOOS == "darwin" {
		deleteErr = deleteMacOSKeychain()
	}
	if err := deletePlaintextCredential(); err != nil && !errors.Is(err, errCredentialNotFound) {
		return err
	}
	if deleteErr != nil && !errors.Is(deleteErr, errCredentialNotFound) {
		return deleteErr
	}
	return nil
}

// saveMacOSKeychain writes credentials into the macOS Keychain.
//
// Args:
//
//	credential: Cloud credential record serialized as one keychain secret.
//
// Returns:
//
//	nil when the security command stores the credential.
func saveMacOSKeychain(credential credentialRecord) error {
	payload, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	cmd := exec.Command("security", "add-generic-password", "-a", keychainAccount, "-s", keychainService, "-w", string(payload), "-U")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("macOS keychain save failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// loadMacOSKeychain reads credentials from the macOS Keychain.
//
// Args:
//
//	None.
//
// Returns:
//
//	Stored cloud credential record.
//
// Errors:
//
//	Returns errCredentialNotFound when the keychain item is absent.
func loadMacOSKeychain() (credentialRecord, error) {
	cmd := exec.Command("security", "find-generic-password", "-a", keychainAccount, "-s", keychainService, "-w")
	output, err := cmd.Output()
	if err != nil {
		return credentialRecord{}, errCredentialNotFound
	}
	var credential credentialRecord
	if err := json.Unmarshal(bytes.TrimSpace(output), &credential); err != nil {
		return credentialRecord{}, err
	}
	return credential, nil
}

// deleteMacOSKeychain removes credentials from the macOS Keychain.
//
// Args:
//
//	None.
//
// Returns:
//
//	nil when the credential is removed or did not exist.
func deleteMacOSKeychain() error {
	cmd := exec.Command("security", "delete-generic-password", "-a", keychainAccount, "-s", keychainService)
	if output, err := cmd.CombinedOutput(); err != nil {
		text := string(output)
		if strings.Contains(text, "could not be found") || strings.Contains(text, "The specified item could not be found") {
			return errCredentialNotFound
		}
		return fmt.Errorf("macOS keychain delete failed: %w: %s", err, strings.TrimSpace(text))
	}
	return nil
}

// savePlaintextCredential writes credentials to an explicit fallback file.
//
// Args:
//
//	credential: Cloud credential record to persist.
//
// Returns:
//
//	nil when the file is written with restrictive permissions.
func savePlaintextCredential(credential credentialRecord) error {
	path, err := plaintextCredentialPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(credential, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

// loadPlaintextCredential reads credentials from an existing fallback file.
//
// Args:
//
//	None.
//
// Returns:
//
//	Stored credential record from the fallback file.
//
// Errors:
//
//	Returns errCredentialNotFound when the fallback file is absent.
func loadPlaintextCredential() (credentialRecord, error) {
	path, err := plaintextCredentialPath()
	if err != nil {
		return credentialRecord{}, err
	}
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return credentialRecord{}, errCredentialNotFound
	}
	if err != nil {
		return credentialRecord{}, err
	}
	var credential credentialRecord
	if err := json.Unmarshal(payload, &credential); err != nil {
		return credentialRecord{}, err
	}
	return credential, nil
}

// deletePlaintextCredential removes the explicit fallback credential file.
//
// Args:
//
//	None.
//
// Returns:
//
//	nil when the file is removed or did not exist.
func deletePlaintextCredential() error {
	path, err := plaintextCredentialPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return errCredentialNotFound
	} else if err != nil {
		return err
	}
	return nil
}

// plaintextCredentialPath returns the fallback credential file path.
//
// Args:
//
//	None.
//
// Returns:
//
//	Absolute path under the user's config directory.
//
// Errors:
//
//	Returns an error when the OS config directory cannot be resolved.
func plaintextCredentialPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "mcpctl", "credentials.json"), nil
}
