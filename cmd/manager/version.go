package main

import (
	"runtime/debug"
	"strings"
)

var (
	version = "v0.0.0-unknown"
)

func SemVer() string {
	semver := version

	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		var (
			revision string
			modified bool
		)
		for _, setting := range buildInfo.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}

		if revision != "" {
			i := len(revision)
			if i > 7 {
				i = 7
			}

			if !strings.Contains(semver, revision[:i]) {
				semver += "+" + revision[:i]
			}
		}

		if modified {
			semver += "*"
		}
	}

	return semver
}
