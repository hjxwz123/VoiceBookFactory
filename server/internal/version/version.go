// Package version identifies the build. The repository's VERSION file is the source of truth;
// release builds also stamp the values below with -ldflags:
//
//	-X voicebook/internal/version.Version=0.1.0
//	-X voicebook/internal/version.Commit=<git sha>
//	-X voicebook/internal/version.BuildTime=<RFC 3339>
package version

import "runtime"

var (
	Version   = "0.1.2" // keep in step with /VERSION (a test checks it)
	Commit    = ""
	BuildTime = ""
)

// Info is what /healthz, the site endpoint and `voicebook version` report.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildTime string `json:"buildTime,omitempty"`
	Go        string `json:"go"`
}

func Get() Info {
	return Info{Version: Version, Commit: Commit, BuildTime: BuildTime, Go: runtime.Version()}
}

// String is a one-line description such as "voicebook 0.1.0 (3f2a9c1, 2026-11-11T08:00:00Z)".
func String() string {
	s := "voicebook " + Version
	switch {
	case Commit != "" && BuildTime != "":
		s += " (" + short(Commit) + ", " + BuildTime + ")"
	case Commit != "":
		s += " (" + short(Commit) + ")"
	}
	return s
}

func short(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}
