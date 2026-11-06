package cli

import (
	"fmt"
	"io"
	"runtime/debug"
)

// version is set at build time (-ldflags "-X github.com/Auugustocesar/caddy-view/internal/cli.version=v1.2.3"). When it
// is not, the version of the module that the Go toolchain recorded is used, which is what `go install` gives.
var version = ""

// buildVersion is the version to show: the one set at build time, else the module's, else "dev".
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func runVersion(stdout io.Writer) int {
	fmt.Fprintf(stdout, "caddy-view %s\n", buildVersion())
	return 0
}
