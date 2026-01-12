// Package version provides build-time version information.
// These variables are set via ldflags during compilation.
package version

// Build-time variables - set via ldflags in Makefile
var (
	// GitCommit is the git commit hash at build time
	GitCommit = "unknown"

	// GitBranch is the git branch at build time
	GitBranch = "unknown"

	// BuildTime is the UTC timestamp when the binary was built
	BuildTime = "unknown"

	// GitDirty indicates if the working directory had uncommitted changes
	GitDirty = "false"
)

// Info returns a formatted version string for logging
func Info() string {
	dirty := ""
	if GitDirty == "true" {
		dirty = " (dirty)"
	}
	return GitCommit + dirty + " (" + GitBranch + ") built " + BuildTime
}
