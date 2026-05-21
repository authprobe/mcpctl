package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// memoryCredentialStore keeps credentials in memory for CLI auth tests.
//
// Args:
//
//	None. Tests configure the store directly.
//
// Returns:
//
//	Methods return the in-memory credential or errCredentialNotFound.
type memoryCredentialStore struct {
	credential credentialRecord
	hasValue   bool
}

// Save stores a credential record in memory for later assertions.
//
// Args:
//
//	credential: Credential value produced by the auth flow.
//
// Returns:
//
//	nil after updating the in-memory record.
func (s *memoryCredentialStore) Save(credential credentialRecord) error {
	s.credential = credential
	s.hasValue = true
	return nil
}

// Load returns the in-memory credential record.
//
// Args:
//
//	None.
//
// Returns:
//
//	Stored credential or errCredentialNotFound when empty.
func (s *memoryCredentialStore) Load() (credentialRecord, error) {
	if !s.hasValue {
		return credentialRecord{}, errCredentialNotFound
	}
	return s.credential, nil
}

// Delete removes the in-memory credential record.
//
// Args:
//
//	None.
//
// Returns:
//
//	nil after clearing the in-memory record.
func (s *memoryCredentialStore) Delete() error {
	s.credential = credentialRecord{}
	s.hasValue = false
	return nil
}

// TestHelpListsPublicCommands verifies that top-level help exposes the local CLI surface.
//
// Args:
//
//	t: Test handle used for fatal assertions.
//
// Returns:
//
//	None. The test fails when expected commands are missing.
func TestHelpListsPublicCommands(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := New(&stdout, &stderr).Run([]string{"--help"})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d", code, exitOK)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	for _, want := range []string{"init", "inspect", "validate", "debug oauth", "auth login", "cloud ping", "registry export", "tunnel"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help output %q missing %q", stdout.String(), want)
		}
	}
}

// TestLocalCommandHelpDescribesDeveloperQuestions verifies local command help is actionable.
//
// Args:
//
//	t: Test handle used for assertions.
//
// Returns:
//
//	None. The test fails when command help omits expected readiness language.
func TestLocalCommandHelpDescribesDeveloperQuestions(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"dev", "--help"}, want: "Run the MCP server"},
		{args: []string{"inspect", "--help"}, want: "Discover tools"},
		{args: []string{"validate", "--help"}, want: "described clearly enough for agents"},
		{args: []string{"auth", "login", "--help"}, want: "browser-based login"},
		{args: []string{"cloud", "ping", "--help"}, want: "without logging in"},
		{args: []string{"debug", "oauth", "--help"}, want: "OAuth discovery diagnostics"},
		{args: []string{"tunnel", "--help"}, want: "managed tunnel"},
		{args: []string{"tunnel", "create", "--help"}, want: "tunnel registration"},
		{args: []string{"tunnel", "run", "--help"}, want: "STDIO MCP server"},
	}

	for _, tc := range cases {
		var stdout bytes.Buffer
		code := New(&stdout, nil).Run(tc.args)
		if code != exitOK {
			t.Fatalf("Run(%v) returned %d, want %d", tc.args, code, exitOK)
		}
		if !strings.Contains(stdout.String(), tc.want) {
			t.Fatalf("Run(%v) output %q missing %q", tc.args, stdout.String(), tc.want)
		}
	}
}

// TestTunnelCreateCallsManagedAPI verifies tunnel creation reuses stored cloud auth.
//
// Args:
//
//	t: Test handle used for HTTP fixture setup and assertions.
//
// Returns:
//
//	None. The test fails when the CLI omits auth or misreads tunnel metadata.
func TestTunnelCreateCallsManagedAPI(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tempHome, ".config"))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	store := &memoryCredentialStore{
		hasValue: true,
		credential: credentialRecord{
			Host:        "https://console.staging.mcpctl.io",
			AccessToken: "operator-token",
			TokenType:   "bearer",
			Source:      "credential-store",
		},
	}
	var gotAuth string
	var gotPayload tunnelCreateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/operator/tunnels" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("Decode request body returned error: %v", err)
		}
		writeJSON(t, w, tunnelResponse{
			TunnelID:   "tun_test",
			ServerID:   "mcp_srv_test",
			GatewayURL: "https://gateway.example/mcp/mcp_srv_test",
			ConnectURL: "wss://gateway.example/tunnel/connect?tunnel_id=tun_test",
			Token:      "tun_secret",
			Status:     "pending",
		})
	}))
	t.Cleanup(server.Close)

	runner := NewWithHTTPClient(&stdout, &stderr, server.Client())
	runner.store = store
	code := runner.Run([]string{"tunnel", "create", "--name", "internal-db", "-endpoint", server.URL})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d; stderr=%q", code, exitOK, stderr.String())
	}
	if gotAuth != "Bearer operator-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotPayload.Name != "internal-db" {
		t.Fatalf("payload = %+v, want tunnel name", gotPayload)
	}
	if !strings.Contains(stdout.String(), "mcpctl tunnel run --server mcp_srv_test -- <command>") {
		t.Fatalf("stdout = %q missing next command", stdout.String())
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir() error = %v", err)
	}
	credentialPath := filepath.Join(configDir, "mcpctl", "tunnels", "tun_test.json")
	if _, err := os.Stat(credentialPath); err != nil {
		t.Fatalf("expected tunnel credential at %s: %v", credentialPath, err)
	}
}

// TestInitWritesConfig verifies that init creates a starter config at the requested path.
//
// Args:
//
//	t: Test handle used for filesystem setup and assertions.
//
// Returns:
//
//	None. The test fails when config creation or output is incorrect.
func TestInitWritesConfig(t *testing.T) {
	var stdout bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "nested", "mcpctl.yaml")

	code := New(&stdout, nil).Run([]string{"init", "-config", configPath})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d", code, exitOK)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(content), "transport:") {
		t.Fatalf("config content %q missing transport section", string(content))
	}
	if !strings.Contains(stdout.String(), "created ") {
		t.Fatalf("stdout = %q, want creation message", stdout.String())
	}
}

// TestInitRefusesOverwrite verifies that init preserves existing config files by default.
//
// Args:
//
//	t: Test handle used for filesystem setup and assertions.
//
// Returns:
//
//	None. The test fails when init overwrites without -force.
func TestInitRefusesOverwrite(t *testing.T) {
	var stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "mcpctl.yaml")
	if err := os.WriteFile(configPath, []byte("existing: true\n"), configFileMode); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	code := New(nil, &stderr).Run([]string{"init", "-config", configPath})
	if code != exitError {
		t.Fatalf("Run returned %d, want %d", code, exitError)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(content) != "existing: true\n" {
		t.Fatalf("config content = %q, want original content", string(content))
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("stderr = %q, want overwrite warning", stderr.String())
	}
}

// TestUnknownCommandUsesUsageExit verifies that invalid command names fail as usage errors.
//
// Args:
//
//	t: Test handle used for assertions.
//
// Returns:
//
//	None. The test fails when invalid commands do not produce usage output.
func TestUnknownCommandUsesUsageExit(t *testing.T) {
	var stderr bytes.Buffer

	code := New(nil, &stderr).Run([]string{"missing"})
	if code != exitUsage {
		t.Fatalf("Run returned %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr = %q, want unknown command diagnostic", stderr.String())
	}
}

// TestVersionIncludesBuildDateWhenAvailable verifies release metadata is visible.
//
// Args:
//
//	t: Test handle used for assertions and global metadata cleanup.
//
// Returns:
//
//	None. The test fails when version output omits injected release date metadata.
func TestVersionIncludesBuildDateWhenAvailable(t *testing.T) {
	previousVersion := Version
	previousCommit := Commit
	previousBuildDate := BuildDate
	Version = "v0.0"
	Commit = "abc1234"
	BuildDate = "2026-05-08T20:53:46Z"
	t.Cleanup(func() {
		Version = previousVersion
		Commit = previousCommit
		BuildDate = previousBuildDate
	})

	var stdout bytes.Buffer
	code := New(&stdout, nil).Run([]string{"version"})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d", code, exitOK)
	}
	want := "mcpctl v0.0 (abc1234, built 2026-05-08T20:53:46Z)\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

// TestDevWithoutConfigSuggestsInit verifies local commands fail with a next action.
//
// Args:
//
//	t: Test handle used for temporary working directory setup and assertions.
//
// Returns:
//
//	None. The test fails when dev omits config setup guidance.
func TestDevWithoutConfigSuggestsInit(t *testing.T) {
	var stderr bytes.Buffer
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("Chdir cleanup returned error: %v", err)
		}
	})
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdir returned error: %v", err)
	}

	code := New(nil, &stderr).Run([]string{"dev"})
	if code != exitError {
		t.Fatalf("Run returned %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr.String(), "run `mcpctl init`") {
		t.Fatalf("stderr = %q, want init guidance", stderr.String())
	}
}

// TestAuthStatusUsesEnvironmentToken verifies auth status can report CI credentials.
//
// Args:
//
//	t: Test handle used for environment isolation and assertions.
//
// Returns:
//
//	None. The test fails when MCPCTL_TOKEN is not identified as the credential source.
func TestAuthStatusUsesEnvironmentToken(t *testing.T) {
	var stdout bytes.Buffer
	t.Setenv("MCPCTL_TOKEN", "test-token")

	code := New(&stdout, nil).Run([]string{"auth", "status"})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout.String(), "MCPCTL_TOKEN") {
		t.Fatalf("stdout = %q, want environment token source", stdout.String())
	}
}

// TestAuthLoginCompletesDeviceFlow verifies browser auth polls and stores tokens.
//
// Args:
//
//	t: Test handle used for local HTTP server setup and assertions.
//
// Returns:
//
//	None. The test fails when login does not complete the device flow.
func TestAuthLoginCompletesDeviceFlow(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	store := &memoryCredentialStore{}
	openedURL := ""
	tokenPolls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli/device-authorizations":
			writeJSON(t, w, deviceAuthorizationResponse{
				DeviceCode:              "device-123",
				UserCode:                "ABCD-1234",
				VerificationURI:         "https://mcpctl.io/device",
				VerificationURIComplete: "https://mcpctl.io/device?user_code=ABCD-1234",
				ExpiresIn:               60,
				Interval:                0,
			})
		case "/v1/cli/token":
			tokenPolls++
			writeJSON(t, w, tokenResponse{
				Status:       "approved",
				AccessToken:  "access-token",
				RefreshToken: "refresh-token",
				TokenType:    "bearer",
				ExpiresAt:    "2026-05-08T18:00:00Z",
				Account:      tokenAccount{Login: "dev"},
				Scopes:       []string{"inspect:write", "account:read"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	runner := NewWithHTTPClient(&stdout, &stderr, server.Client())
	runner.store = store
	runner.sleep = func(time.Duration) {}
	runner.openURL = func(target string) error {
		openedURL = target
		return nil
	}

	code := runner.Run([]string{"auth", "login", "-endpoint", server.URL})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d; stderr=%q", code, exitOK, stderr.String())
	}
	if tokenPolls != 1 {
		t.Fatalf("token polls = %d, want 1", tokenPolls)
	}
	if openedURL == "" {
		t.Fatal("browser URL was not opened")
	}
	if !store.hasValue {
		t.Fatal("credential was not stored")
	}
	if store.credential.AccessToken != "access-token" || store.credential.RefreshToken != "refresh-token" {
		t.Fatalf("stored credential = %+v, want access and refresh tokens", store.credential)
	}
	if strings.Contains(stdout.String(), "access-token") || strings.Contains(stdout.String(), "refresh-token") {
		t.Fatalf("stdout leaked token material: %q", stdout.String())
	}
}

// TestResolveCloudEndpointSupportsStagingProfile verifies hosted commands can target staging by environment.
//
// Args:
//
//	t: Test handle used for endpoint resolution assertions.
//
// Returns:
//
//	None.
//
// Errors:
//
//	Fails when MCPCTL_ENV or MCPCTL_ENDPOINT does not select the expected endpoint.
func TestResolveCloudEndpointSupportsStagingProfile(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "default", env: map[string]string{}, want: defaultCloud},
		{name: "staging profile", env: map[string]string{envCloudProfile: "staging"}, want: stagingCloud},
		{name: "explicit endpoint", env: map[string]string{envCloudEndpoint: "https://console.preview.example/"}, want: "https://console.preview.example"},
		{name: "legacy endpoint alias", env: map[string]string{envCloudEndpoint2: "https://console.alias.example/"}, want: "https://console.alias.example"},
	}
	for _, tc := range cases {
		got := resolveCloudEndpoint(func(key string) string {
			return tc.env[key]
		})
		if got != tc.want {
			t.Fatalf("%s: resolveCloudEndpoint() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestAuthLoginInteractiveBrowserOnly verifies terminal login uses host and browser prompts.
//
// Args:
//
//	t: Test handle used for local HTTP server setup and assertions.
//
// Returns:
//
//	None. The test fails when interactive login exposes a non-browser choice.
func TestAuthLoginInteractiveBrowserOnly(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	store := &memoryCredentialStore{}
	openedURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli/device-authorizations":
			writeJSON(t, w, deviceAuthorizationResponse{
				DeviceCode:              "device-123",
				UserCode:                "ABCD-1234",
				VerificationURI:         "https://mcpctl.io/device",
				VerificationURIComplete: "https://mcpctl.io/device?user_code=ABCD-1234",
				ExpiresIn:               60,
				Interval:                0,
			})
		case "/v1/cli/token":
			writeJSON(t, w, tokenResponse{
				Status:      "approved",
				AccessToken: "access-token",
				TokenType:   "bearer",
				Account:     tokenAccount{Login: "dev"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	runner := NewWithHTTPClient(&stdout, &stderr, server.Client())
	runner.store = store
	runner.sleep = func(time.Duration) {}
	runner.openURL = func(target string) error {
		openedURL = target
		return nil
	}
	runner.stdin = strings.NewReader("\n")
	runner.interactive = true

	code := runner.Run([]string{"auth", "login", "-endpoint", server.URL})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d; stderr=%q", code, exitOK, stderr.String())
	}
	if openedURL != "https://mcpctl.io/device?user_code=ABCD-1234" {
		t.Fatalf("opened URL = %q, want verification URL", openedURL)
	}
	output := stdout.String()
	for _, want := range []string{"Where do you use mcpctl?", "First copy your one-time code", "Press Enter to open"} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, want %q", output, want)
		}
	}
	for _, unwanted := range []string{"SSH", "Paste an authentication token"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("stdout = %q, did not want %q", output, unwanted)
		}
	}
}

// TestAuthLoginOmitsHTMLFailureBody verifies frontend 404 pages stay concise.
//
// Args:
//
//	t: Test handle used for local HTTP server setup and assertions.
//
// Returns:
//
//	None. The test fails when a large HTML error body leaks to stderr.
func TestAuthLoginOmitsHTMLFailureBody(t *testing.T) {
	var stderr bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		if _, err := w.Write([]byte("<!DOCTYPE html><html><head><title>404</title></head><body>large frontend page</body></html>")); err != nil {
			t.Fatalf("Write returned error: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	runner := NewWithHTTPClient(nil, &stderr, server.Client())
	code := runner.Run([]string{"auth", "login", "-endpoint", server.URL})
	if code != exitError {
		t.Fatalf("Run returned %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr.String(), "HTML response omitted") {
		t.Fatalf("stderr = %q, want HTML placeholder", stderr.String())
	}
	if strings.Contains(stderr.String(), "<!DOCTYPE html>") || strings.Contains(stderr.String(), "large frontend page") {
		t.Fatalf("stderr leaked HTML body: %q", stderr.String())
	}
}

// TestAuthLoginDeniedDoesNotStoreCredential verifies denied browser approval fails safely.
//
// Args:
//
//	t: Test handle used for local HTTP server setup and assertions.
//
// Returns:
//
//	None. The test fails when denied login stores credentials.
func TestAuthLoginDeniedDoesNotStoreCredential(t *testing.T) {
	var stderr bytes.Buffer
	store := &memoryCredentialStore{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli/device-authorizations":
			writeJSON(t, w, deviceAuthorizationResponse{
				DeviceCode:      "device-123",
				UserCode:        "ABCD-1234",
				VerificationURI: "https://mcpctl.io/device",
				ExpiresIn:       60,
				Interval:        0,
			})
		case "/v1/cli/token":
			writeJSON(t, w, tokenResponse{Status: "access_denied"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	runner := NewWithHTTPClient(nil, &stderr, server.Client())
	runner.store = store
	runner.sleep = func(time.Duration) {}
	runner.openURL = func(string) error { return nil }

	code := runner.Run([]string{"auth", "login", "-endpoint", server.URL, "-web=false"})
	if code != exitError {
		t.Fatalf("Run returned %d, want %d", code, exitError)
	}
	if store.hasValue {
		t.Fatalf("credential stored after denial: %+v", store.credential)
	}
	if !strings.Contains(stderr.String(), "access denied") {
		t.Fatalf("stderr = %q, want access denied", stderr.String())
	}
}

// TestAuthStatusReadsStoredCredential verifies status reports credential-store identity.
//
// Args:
//
//	t: Test handle used for assertions.
//
// Returns:
//
//	None. The test fails when stored credentials are not reflected in status output.
func TestAuthStatusReadsStoredCredential(t *testing.T) {
	var stdout bytes.Buffer
	store := &memoryCredentialStore{
		hasValue: true,
		credential: credentialRecord{
			Host:         "https://mcpctl.io",
			AccountLogin: "dev",
			Scopes:       []string{"account:read"},
			Source:       "credential-store",
		},
	}
	runner := New(&stdout, nil)
	runner.store = store

	code := runner.Run([]string{"auth", "status"})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout.String(), "dev") || !strings.Contains(stdout.String(), "account:read") {
		t.Fatalf("stdout = %q, want stored credential details", stdout.String())
	}
}

// TestAuthLogoutRevokesAndDeletesCredential verifies logout revokes refresh tokens.
//
// Args:
//
//	t: Test handle used for local HTTP server setup and assertions.
//
// Returns:
//
//	None. The test fails when logout omits revocation or local deletion.
func TestAuthLogoutRevokesAndDeletesCredential(t *testing.T) {
	var stdout bytes.Buffer
	store := &memoryCredentialStore{
		hasValue: true,
		credential: credentialRecord{
			Host:         "https://mcpctl.io",
			RefreshToken: "refresh-token",
		},
	}
	revoked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/cli/token/revoke" {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if body["refresh_token"] != "refresh-token" {
			t.Fatalf("refresh_token = %q, want refresh-token", body["refresh_token"])
		}
		revoked = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	runner := NewWithHTTPClient(&stdout, nil, server.Client())
	runner.store = store

	code := runner.Run([]string{"auth", "logout", "-endpoint", server.URL})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d", code, exitOK)
	}
	if !revoked {
		t.Fatal("refresh token was not revoked")
	}
	if store.hasValue {
		t.Fatal("credential was not deleted")
	}
	if !strings.Contains(stdout.String(), "Logged out") {
		t.Fatalf("stdout = %q, want logout confirmation", stdout.String())
	}
}

// TestCloudPingUsesNoLoginEndpoint verifies cloud ping reaches an endpoint without auth.
//
// Args:
//
//	t: Test handle used for local HTTP server setup and assertions.
//
// Returns:
//
//	None. The test fails when cloud ping does not call the configured endpoint.
func TestCloudPingUsesNoLoginEndpoint(t *testing.T) {
	var stdout bytes.Buffer
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization header = %q, want empty", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	code := New(&stdout, nil).Run([]string{"cloud", "ping", "-endpoint", server.URL})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d", code, exitOK)
	}
	if !called {
		t.Fatal("cloud ping did not call test server")
	}
	if !strings.Contains(stdout.String(), "reachable") {
		t.Fatalf("stdout = %q, want reachable message", stdout.String())
	}
}

// TestDebugOAuthCreatesHostedCompatibilityRun verifies OAuth debugging publishes a compatibility trace.
//
// Args:
//
//	t: Test handle used for HTTP fixture setup and assertions.
//
// Returns:
//
//	None. The test fails when the CLI omits auth, sends the wrong probes, or hides trace URLs.
func TestDebugOAuthCreatesHostedCompatibilityRun(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	store := &memoryCredentialStore{
		hasValue: true,
		credential: credentialRecord{
			Host:        "https://console.staging.mcpctl.io",
			AccessToken: "operator-token",
			TokenType:   "bearer",
			Source:      "credential-store",
		},
	}
	var gotAuth string
	var gotPayload debugConnectRunRequest
	var gotMCPMethods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+externalURL(r, "/.well-known/oauth-protected-resource/mcp")+`"`)
			http.Error(w, "missing token", http.StatusUnauthorized)
		case "/.well-known/oauth-protected-resource/mcp":
			writeJSON(t, w, map[string]any{
				"resource":              externalURL(r, "/mcp"),
				"authorization_servers": []string{externalURL(r, "/login/oauth")},
			})
		case "/.well-known/oauth-authorization-server/login/oauth":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint":           externalURL(r, "/login/oauth/authorize"),
				"token_endpoint":                   externalURL(r, "/login/oauth/access_token"),
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/v1/operator/compat/runs":
			gotAuth = r.Header.Get("Authorization")
			if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
				t.Fatalf("Decode request body returned error: %v", err)
			}
			writeJSON(t, w, debugConnectRunResponse{
				RunID:      "crun_test",
				Status:     "failed",
				TraceURL:   externalURL(r, "/compat/trace/crun_test/mcp/"),
				ReportURL:  "https://console.staging.mcpctl.io/compat/r/crun_test",
				GatewayURL: externalURL(r, "/compat/gateway/crun_test/mcp/"),
			})
		case "/compat/gateway/crun_test/mcp/":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode MCP probe body returned error: %v", err)
			}
			method, _ := payload["method"].(string)
			gotMCPMethods = append(gotMCPMethods, method)
			if method == "initialize" {
				w.Header().Set("Mcp-Session-Id", "session-test")
				writeJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"result":  map[string]any{"protocolVersion": "2025-11-25"},
				})
				return
			}
			if r.Header.Get("Mcp-Session-Id") != "session-test" {
				t.Fatalf("tools/list Mcp-Session-Id = %q, want session-test", r.Header.Get("Mcp-Session-Id"))
			}
			writeJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      2,
				"result":  map[string]any{"tools": []any{}},
			})
		default:
			http.NotFound(w, r)
			return
		}
	}))
	t.Cleanup(server.Close)

	runner := NewWithHTTPClient(&stdout, &stderr, server.Client())
	runner.store = store

	code := runner.Run([]string{
		"debug",
		"oauth",
		server.URL + "/mcp",
		"--client",
		"chatgpt",
		"--share",
		"-endpoint",
		server.URL,
	})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d; stderr=%q", code, exitOK, stderr.String())
	}
	if gotAuth != "Bearer operator-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotPayload.TargetURL != server.URL+"/mcp" {
		t.Fatalf("TargetURL = %q, want target URL", gotPayload.TargetURL)
	}
	if gotPayload.ClientProfile != "chatgpt" || gotPayload.Mode != "gateway" {
		t.Fatalf("unexpected compat payload: %+v", gotPayload)
	}
	if gotPayload.UpstreamMode != "proxy" || !gotPayload.Shareable {
		t.Fatalf("unexpected upstream/share fields: %+v", gotPayload)
	}
	if strings.Join(gotMCPMethods, ",") != "initialize,tools/list" {
		t.Fatalf("MCP probe methods = %#v, want initialize then tools/list", gotMCPMethods)
	}
	for _, probe := range []string{"oauth_discovery", "mcp_initialize", "tools_list"} {
		if !containsString(gotPayload.SelectedProbes, probe) {
			t.Fatalf("SelectedProbes = %#v missing %q", gotPayload.SelectedProbes, probe)
		}
	}
	for _, want := range []string{
		"https://console.staging.mcpctl.io/compat/r/crun_test",
		"Debug Inbox",
		"managed MCP server",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q missing %q", stdout.String(), want)
		}
	}
	for _, unwanted := range []string{"Trace URL:", "Gateway URL:", "MCP initialize:", "MCP tools/list:"} {
		if strings.Contains(stdout.String(), unwanted) {
			t.Fatalf("stdout = %q unexpectedly contained %q", stdout.String(), unwanted)
		}
	}
}

// TestDebugOAuthShareUnauthorizedPrintsLoginHint verifies expired cloud auth is actionable.
//
// Args:
//
//	t: Test handle used for HTTP fixture setup and assertions.
//
// Returns:
//
//	None. The test fails when a hosted share 401 omits the environment-specific login command.
func TestDebugOAuthShareUnauthorizedPrintsLoginHint(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	store := &memoryCredentialStore{
		hasValue: true,
		credential: credentialRecord{
			Host:        stagingCloud,
			AccessToken: "expired-token",
			TokenType:   "bearer",
			Source:      "credential-store",
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+externalURL(r, "/.well-known/oauth-protected-resource/mcp")+`"`)
			http.Error(w, "missing token", http.StatusUnauthorized)
		case "/.well-known/oauth-protected-resource/mcp":
			writeJSON(t, w, map[string]any{
				"resource":              externalURL(r, "/mcp"),
				"authorization_servers": []string{externalURL(r, "/login/oauth")},
			})
		case "/.well-known/oauth-authorization-server/login/oauth":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": externalURL(r, "/login/oauth/authorize"),
				"token_endpoint":         externalURL(r, "/login/oauth/access_token"),
			})
		case "/v1/operator/compat/runs":
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(t, w, map[string]any{
				"error": map[string]any{
					"code":    "unauthorized",
					"message": "operator auth unauthorized",
				},
			})
		default:
			http.NotFound(w, r)
			return
		}
	}))
	t.Cleanup(server.Close)

	runner := NewWithHTTPClient(&stdout, &stderr, server.Client())
	runner.store = store

	code := runner.Run([]string{
		"debug",
		"oauth",
		server.URL + "/mcp",
		"--client",
		"chatgpt",
		"--share",
		"-endpoint",
		stagingCloud,
	})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d; stderr=%q", code, exitOK, stderr.String())
	}
	for _, want := range []string{
		"OAuth debug",
		"ChatGPT note:",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q missing %q", stdout.String(), want)
		}
	}
	for _, want := range []string{
		"warning: hosted share failed:",
		"operator auth unauthorized",
		"run `MCPCTL_ENV=staging mcpctl auth login` and retry --share",
		"401 Unauthorized",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q missing %q", stderr.String(), want)
		}
	}
}

// TestDebugConnectCreatesHostedCompatibilityRun verifies the CLI calls the managed compatibility API.
//
// Args:
//
//	t: Test handle used for HTTP fixture setup and assertions.
//
// Returns:
//
//	None. The test fails when the CLI omits auth, sends the wrong payload, or hides run URLs.
func TestDebugConnectCreatesHostedCompatibilityRun(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	store := &memoryCredentialStore{
		hasValue: true,
		credential: credentialRecord{
			Host:        "https://console.staging.mcpctl.io",
			AccessToken: "operator-token",
			TokenType:   "bearer",
			Source:      "credential-store",
		},
	}
	var gotAuth string
	var gotPayload debugConnectRunRequest
	var gotMCPMethods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/operator/compat/runs":
			gotAuth = r.Header.Get("Authorization")
			if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
				t.Fatalf("Decode request body returned error: %v", err)
			}
			writeJSON(t, w, debugConnectRunResponse{
				RunID:      "crun_test",
				Status:     "failed",
				TraceURL:   externalURL(r, "/compat/trace/crun_test/mcp/"),
				ReportURL:  "https://console.staging.mcpctl.io/compat/r/crun_test",
				GatewayURL: externalURL(r, "/compat/gateway/crun_test/mcp/"),
			})
		case "/compat/gateway/crun_test/mcp/":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode MCP probe body returned error: %v", err)
			}
			method, _ := payload["method"].(string)
			gotMCPMethods = append(gotMCPMethods, method)
			if method == "initialize" {
				w.Header().Set("Mcp-Session-Id", "session-test")
				writeJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      1,
					"result":  map[string]any{"protocolVersion": "2025-11-25"},
				})
				return
			}
			if r.Header.Get("Mcp-Session-Id") != "session-test" {
				t.Fatalf("tools/list Mcp-Session-Id = %q, want session-test", r.Header.Get("Mcp-Session-Id"))
			}
			writeJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      2,
				"result":  map[string]any{"tools": []any{}},
			})
		default:
			http.NotFound(w, r)
			return
		}
	}))
	t.Cleanup(server.Close)

	runner := NewWithHTTPClient(&stdout, &stderr, server.Client())
	runner.store = store

	code := runner.Run([]string{
		"debug",
		"connect",
		server.URL + "/mcp",
		"--client",
		"chatgpt",
		"--mode",
		"gateway",
		"--share",
		"-endpoint",
		server.URL,
	})
	if code != exitOK {
		t.Fatalf("Run returned %d, want %d; stderr=%q", code, exitOK, stderr.String())
	}
	if gotAuth != "Bearer operator-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotPayload.TargetURL != server.URL+"/mcp" || gotPayload.ClientProfile != "chatgpt" || gotPayload.Mode != "gateway" {
		t.Fatalf("unexpected compat payload: %+v", gotPayload)
	}
	if gotPayload.UpstreamMode != "proxy" || !gotPayload.Shareable {
		t.Fatalf("unexpected upstream/share fields: %+v", gotPayload)
	}
	if strings.Join(gotMCPMethods, ",") != "initialize,tools/list" {
		t.Fatalf("MCP probe methods = %#v, want initialize then tools/list", gotMCPMethods)
	}
	for _, probe := range []string{"oauth_discovery", "mcp_initialize", "tools_list"} {
		if !containsString(gotPayload.SelectedProbes, probe) {
			t.Fatalf("SelectedProbes = %#v missing %q", gotPayload.SelectedProbes, probe)
		}
	}
	for _, want := range []string{"Report:", "https://console.staging.mcpctl.io/compat/r/crun_test", "Debug Inbox", "managed MCP server"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q missing %q", stdout.String(), want)
		}
	}
	for _, unwanted := range []string{"Trace URL:", "Gateway URL:", "MCP initialize:", "MCP tools/list:"} {
		if strings.Contains(stdout.String(), unwanted) {
			t.Fatalf("stdout = %q unexpectedly contained %q", stdout.String(), unwanted)
		}
	}
}

// externalURL builds an absolute URL for the current httptest request host.
//
// Args:
//
//	r: Incoming request whose Host identifies the test server.
//	path: Absolute path to attach to the server origin.
//
// Returns:
//
//	A test-server URL suitable for fixture payloads.
func externalURL(r *http.Request, path string) string {
	return "http://" + r.Host + path
}

// containsString reports whether a list includes one exact value.
//
// Args:
//
//	values: Candidate values from CLI output or request payloads.
//	want: Exact value to find.
//
// Returns:
//
//	True when want appears in values.
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// writeJSON writes a JSON response for auth flow fixtures.
//
// Args:
//
//	t: Test handle used for fatal encoding assertions.
//	w: Response writer receiving JSON.
//	value: JSON-serializable response value.
//
// Returns:
//
//	None. The test fails when encoding cannot complete.
func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		t.Fatalf("Encode returned error: %v", err)
	}
}
