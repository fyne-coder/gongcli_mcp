package version

import (
	"runtime/debug"
	"strings"
)

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func Current() Info {
	info := Info{
		Version: clean(Version, "dev"),
		Commit:  clean(Commit, "unknown"),
		Date:    clean(Date, "unknown"),
	}

	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "dev" {
			if value := clean(buildInfo.Main.Version, ""); value != "" && value != "(devel)" {
				info.Version = value
			}
		}
		for _, setting := range buildInfo.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.Commit == "unknown" {
					info.Commit = clean(setting.Value, "unknown")
				}
			case "vcs.time":
				if info.Date == "unknown" {
					info.Date = clean(setting.Value, "unknown")
				}
			}
		}
	}

	return info
}

func DisplayVersion() string {
	return Current().Version
}

func clean(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
