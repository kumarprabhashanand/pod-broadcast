package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// version is set at release time with -ldflags "-X <module>/internal/version.version=vX.Y.Z".
var version string

// Line is what a version command prints, such as "kubectl-broadcast v0.1.0 (go1.27.3 linux/amd64)".
func Line(program string) string {
	return fmt.Sprintf("%s %s (%s %s/%s)", program, String(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// String is the release version, the module version for go install builds, or "dev".
func String() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
