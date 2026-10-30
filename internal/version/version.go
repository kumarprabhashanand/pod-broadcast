package version

import "runtime/debug"

// version is set at release time with -ldflags "-X <module>/internal/version.version=vX.Y.Z".
var version string

func String() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
