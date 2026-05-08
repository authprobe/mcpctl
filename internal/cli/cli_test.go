package cli

import (
	"bytes"
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

	for _, want := range []string{"init", "inspect", "validate", "registry export"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help output %q missing %q", stdout.String(), want)
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
