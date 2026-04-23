package output

import "testing"

func TestSupportsANSIRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "")
	if supportsANSI() {
		t.Fatal("expected NO_COLOR to disable ANSI output")
	}
}

func TestSupportsANSIRespectsForceColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")
	if !supportsANSI() {
		t.Fatal("expected FORCE_COLOR to enable ANSI output")
	}
}
