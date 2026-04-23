package platform

import (
	"path/filepath"
	"testing"
)

func TestBackupRootUsesProjectDirectoryName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, want := BackupRoot(), filepath.Join(home, ".claude-account-backup"); got != want {
		t.Fatalf("unexpected backup root: got %q want %q", got, want)
	}
}

