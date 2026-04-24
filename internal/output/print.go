package output

import (
	"encoding/json"
	"fmt"
	"strings"
	"os"
)

func PrintJSON(v any) {
	body, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(body))
}

func Info(msg string, args ...any) {
	fmt.Printf(msg+"\n", args...)
}

func Bold(s string) string {
	return style("1", s)
}

func Dim(s string) string {
	return style("2", s)
}

func Italic(s string) string {
	return style("3", s)
}

func Cyan(s string) string {
	return style("36", s)
}

func Green(s string) string {
	return style("32", s)
}

func Accent(s string) string {
	return style("38;5;173", s)
}

func Muted(s string) string {
	return style("38;5;250", s)
}

func Yellow(s string) string {
	return style("33", s)
}

func Red(s string) string {
	return style("31", s)
}

func style(code, s string) string {
	if !supportsANSI() || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func supportsANSI() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	term := strings.TrimSpace(os.Getenv("TERM"))
	if term == "" || term == "dumb" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
