package version

import (
	"os"
	"strings"
	"testing"
)

// The default version must match the repository's VERSION file, so a dev build reports the right number.
func TestMatchesVersionFile(t *testing.T) {
	b, err := os.ReadFile("../../../VERSION")
	if err != nil {
		t.Skip("VERSION file not found:", err)
	}
	if want := strings.TrimSpace(string(b)); Version != want {
		t.Fatalf("version.Version = %q, VERSION file says %q", Version, want)
	}
}

func TestString(t *testing.T) {
	old := Commit
	defer func() { Commit = old }()
	Commit = "3f2a9c1d0e"
	if s := String(); !strings.Contains(s, "voicebook "+Version) || !strings.Contains(s, "3f2a9c1") || strings.Contains(s, "3f2a9c1d") {
		t.Fatalf("String() = %q", s)
	}
}
