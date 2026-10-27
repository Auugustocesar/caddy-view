package store

import (
	"errors"
	"os"
	"runtime"
	"strings"
)

const appDirName = "caddy-view"

// errNoDataHome means the environment does not say where the user's data directory is.
var errNoDataHome = errors.New("cannot find the user data directory: set --data-dir")

// DefaultDataDir is where caddy-view keeps its database when --data-dir is not given: the user's data directory
// plus "caddy-view".
func DefaultDataDir() (string, error) { return dataDir(runtime.GOOS, os.Getenv) }

// dataDir computes DefaultDataDir for a given operating system and environment, so every platform can be tested
// from any platform.
func dataDir(goos string, getenv func(string) string) (string, error) {
	switch goos {
	case "windows":
		base := getenv("LocalAppData")
		if base == "" {
			return "", errNoDataHome
		}
		return base + `\` + appDirName, nil
	case "darwin":
		home := getenv("HOME")
		if home == "" {
			return "", errNoDataHome
		}
		return home + "/Library/Application Support/" + appDirName, nil
	default:
		// The XDG specification says a relative XDG_DATA_HOME must be ignored.
		if xdg := getenv("XDG_DATA_HOME"); strings.HasPrefix(xdg, "/") {
			return xdg + "/" + appDirName, nil
		}
		home := getenv("HOME")
		if home == "" {
			return "", errNoDataHome
		}
		return home + "/.local/share/" + appDirName, nil
	}
}
