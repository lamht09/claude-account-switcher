package platform

import (
	"os"
	"path/filepath"
)

func HomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

func ClaudeConfigHome() string {
	if c := os.Getenv("CLAUDE_CONFIG_DIR"); c != "" {
		return c
	}
	return filepath.Join(HomeDir(), ".claude")
}

func GlobalConfigPath() string {
	legacy := filepath.Join(ClaudeConfigHome(), ".config.json")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	base := HomeDir()
	if c := os.Getenv("CLAUDE_CONFIG_DIR"); c != "" {
		base = c
	}
	return filepath.Join(base, ".claude.json")
}

func LiveCredentialPath() string {
	return filepath.Join(ClaudeConfigHome(), ".credentials.json")
}

func BackupRoot() string {
	return filepath.Join(HomeDir(), ".claude-account-backup")
}
