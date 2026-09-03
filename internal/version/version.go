// Package version exposes the build-time version string. Set with
// -ldflags "-X github.com/mmedum/google-docs-mcp/internal/version.Version=v1.2.3".
package version

import (
	"fmt"
	"runtime"
)

// Version is the semantic version of the binary. "dev" for local builds.
var Version = "dev"

// Info returns a one-line description suitable for --version output.
func Info() string {
	return fmt.Sprintf("google-docs-mcp %s (%s %s/%s)", Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
