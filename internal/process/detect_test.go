package process

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunningSessionsAndIDEInstances(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	if err := os.MkdirAll(filepath.Join(claudeDir, "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(claudeDir, "ide"), 0o700); err != nil {
		t.Fatalf("mkdir ide: %v", err)
	}
	pid := os.Getpid()
	if !isAlive(pid) {
		t.Skip("current process PID is not visible in this environment")
	}
	sessionPath := filepath.Join(claudeDir, "sessions", "s1.json")
	sessionBody, _ := json.Marshal(Session{
		PID:        pid,
		CWD:        "/tmp/project",
		Entrypoint: "cli",
	})
	if err := os.WriteFile(sessionPath, sessionBody, 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}

	idePath := filepath.Join(claudeDir, "ide", "31337.lock")
	ideBody, _ := json.Marshal(IDEInstance{
		PID:              pid,
		IDEName:          "Visual Studio Code",
		WorkspaceFolders: []string{"/tmp/project"},
	})
	if err := os.WriteFile(idePath, ideBody, 0o600); err != nil {
		t.Fatalf("write ide: %v", err)
	}

	sessions := RunningSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	ides := RunningIDEInstances()
	if len(ides) != 1 {
		t.Fatalf("expected 1 ide instance, got %d", len(ides))
	}
	if ides[0].Port != 31337 {
		t.Fatalf("expected port 31337, got %d", ides[0].Port)
	}
}
