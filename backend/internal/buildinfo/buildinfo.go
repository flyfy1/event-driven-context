// Package buildinfo exposes release metadata embedded in the server and CLI.
package buildinfo

const (
	// Version is intentionally source-controlled so a server built from the same
	// release can recommend the matching CLI without contacting a third party.
	DefaultVersion    = "v0.1.0"
	MinimumCLIVersion = "v0.1.0"
	ReleaseAPIURL     = "https://api.github.com/repos/flyfy1/event-driven-context/releases/latest"
	ReleasePageURL    = "https://github.com/flyfy1/event-driven-context/releases/latest"
)

// These values may be replaced with -ldflags at build time.
var (
	Version = DefaultVersion
	Commit  = "unknown"
	BuiltAt = "unknown"
)

type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at"`
}

type CLIPolicy struct {
	LatestVersion  string `json:"latest_version"`
	MinimumVersion string `json:"minimum_version"`
	ReleaseAPIURL  string `json:"release_api_url"`
	ReleasePageURL string `json:"release_page_url"`
}

func Current() Info {
	return Info{Version: Version, Commit: Commit, BuiltAt: BuiltAt}
}

func CurrentCLIPolicy() CLIPolicy {
	return CLIPolicy{
		LatestVersion:  Version,
		MinimumVersion: MinimumCLIVersion,
		ReleaseAPIURL:  ReleaseAPIURL,
		ReleasePageURL: ReleasePageURL,
	}
}
