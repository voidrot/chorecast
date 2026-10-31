// Package version holds build metadata injected via -ldflags.
package version

var (
	Version = "dev"
	Commit  = "none"
)

// APIVersion is bumped on breaking API changes; the Android app checks it for compatibility.
const APIVersion = 1
