package process

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/lamht09/claude-account-switcher/internal/platform"
)

type Session struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	CWD        string `json:"cwd"`
	Status     string `json:"status"`
	Kind       string `json:"kind"`
	Entrypoint string `json:"entrypoint"`
}

type IDEInstance struct {
	PID              int      `json:"pid"`
	IDEName          string   `json:"ideName"`
	WorkspaceFolders []string `json:"workspaceFolders"`
	Port             int
}

func isAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	return isPIDAlivePlatform(pid)
}

func RunningSessions() []Session {
	dir := filepath.Join(platform.ClaudeConfigHome(), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []Session{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		if isAlive(s.PID) {
			out = append(out, s)
		}
	}
	return out
}

func RunningIDEPorts() []int {
	dir := filepath.Join(platform.ClaudeConfigHome(), "ide")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []int{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".lock" {
			continue
		}
		port, err := strconv.Atoi(e.Name()[:len(e.Name())-len(".lock")])
		if err == nil {
			out = append(out, port)
		}
	}
	return out
}

func RunningIDEInstances() []IDEInstance {
	dir := filepath.Join(platform.ClaudeConfigHome(), "ide")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []IDEInstance{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".lock" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var ide IDEInstance
		if err := json.Unmarshal(raw, &ide); err != nil {
			continue
		}
		if !isAlive(ide.PID) {
			continue
		}
		port, err := strconv.Atoi(e.Name()[:len(e.Name())-len(".lock")])
		if err == nil {
			ide.Port = port
		}
		out = append(out, ide)
	}
	return out
}
