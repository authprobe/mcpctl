package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestTunnelContractFixturesDecode verifies shared tunnel fixtures match CLI structs.
//
// Args:
//
//	t: Test handle used for fixture reads and assertions.
//
// Returns:
//
//	None. The test fails when shared tunnel JSON contracts drift from CLI types.
func TestTunnelContractFixturesDecode(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "tunnel", "v1")
	var registration tunnelResponse
	readTunnelFixture(t, filepath.Join(root, "create_response.json"), &registration)
	if registration.TunnelID != "tun_example" || registration.Status != "pending" {
		t.Fatalf("unexpected registration fixture: %+v", registration)
	}
	var frame struct {
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		ServerID  string          `json:"server_id"`
		Payload   json.RawMessage `json:"payload"`
	}
	readTunnelFixture(t, filepath.Join(root, "frame_request.json"), &frame)
	if frame.Type != "request" || frame.RequestID != "req_example" || len(frame.Payload) == 0 {
		t.Fatalf("unexpected frame fixture: %+v", frame)
	}
}

// readTunnelFixture decodes one tunnel JSON fixture into a target struct.
//
// Args:
//
//	t: Test handle used to fail immediately on read/decode errors.
//	path: Repository-relative fixture path.
//	target: Pointer to the struct that should receive decoded JSON.
//
// Returns:
//
//	None.
func readTunnelFixture(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", path, err)
	}
}
