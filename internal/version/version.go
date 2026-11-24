// Package version carries build metadata.
package version

// Values are overridden at build time via -ldflags "-X walky/internal/version.Version=...".
var (
	// Version is the human-facing release version.
	Version = "0.1.0-dev"
	// Commit is the git commit the binary was built from.
	Commit = "unknown"
	// Date is the build date in RFC 3339 form.
	Date = "unknown"
)

// String returns a single-line version string.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
