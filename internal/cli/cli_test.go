package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	for _, want := range []string{"init", "inspect", "validate", "auth login", "cloud ping", "registry export"} {
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
