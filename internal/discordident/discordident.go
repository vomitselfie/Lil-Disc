// Package discordident builds the client identity LilDisc presents to
// Discord: the HTTP User-Agent, the X-Super-Properties and X-Discord-Locale
// headers, and the matching gateway IDENTIFY properties.
//
// # Why this exists
//
// Discord's official desktop client is Electron, so every REST call it makes
// carries a Chrome User-Agent plus an X-Super-Properties header describing the
// build. Arikawa sends neither: its default User-Agent names the library, and
// it has no concept of super properties. A user token driving REST calls with
// a library User-Agent and no super properties is the shape Discord's abuse
// tooling uses to pick out automated clients, and attachment uploads are where
// that scoring bites hardest, because uploads are the vector for malware and
// unsolicited imagery.
//
// # One source of truth
//
// Every string below is derived from the version constants in this file, so
// the User-Agent and the super properties cannot drift apart. A blob that
// contradicts its own User-Agent is a stronger tell than no blob at all, so
// nothing here should be edited in isolation.
//
// # Keeping it current
//
// The build numbers and versions go stale as Discord ships, and a build
// number far behind current stable is itself detectable. Every one of them can
// be overridden from ~/.config/lildisc/env without rebuilding:
//
//	LILDISC_CLIENT_VERSION=0.0.140
//	LILDISC_CLIENT_BUILD_NUMBER=421337
//	LILDISC_NATIVE_BUILD_NUMBER=70201
//	LILDISC_CHROME_VERSION=152.0.7014.35
//	LILDISC_ELECTRON_VERSION=39.1.2
//
// To read the current values, open the official client (or the web app) with
// devtools, take any request to discord.com/api, and copy its User-Agent and
// the base64 X-Super-Properties payload.
//
// Setting LILDISC_CLIENT_IDENTITY=off restores the old behaviour: a
// self-identifying User-Agent naming this project, and no super properties.
package discordident

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Defaults for the identity presented to Discord. These describe the official
// Discord desktop client for Linux. They are a coherent set: clientVersion is
// what appears as discord/<version> in the User-Agent, chromeVersion and
// electronVersion are the runtime underneath it, and the build numbers are
// what the same release reports in its super properties.
//
// See the package comment for how to refresh these.
const (
	clientVersion   = "0.0.130"
	chromeVersion   = "140.0.7339.207"
	electronVersion = "37.2.6"

	clientBuildNumber = 371072
	nativeBuildNumber = 68352

	releaseChannel = "stable"
)

// Environment variables that override the constants above, so a stale build
// number is a config edit rather than a release.
const (
	envDisable         = "LILDISC_CLIENT_IDENTITY"
	envClientVersion   = "LILDISC_CLIENT_VERSION"
	envChromeVersion   = "LILDISC_CHROME_VERSION"
	envElectronVersion = "LILDISC_ELECTRON_VERSION"
	envBuildNumber     = "LILDISC_CLIENT_BUILD_NUMBER"
	envNativeBuild     = "LILDISC_NATIVE_BUILD_NUMBER"
)

// legacyUserAgent is what LilDisc sent before this package existed. It is
// kept for LILDISC_CLIENT_IDENTITY=off, which is the honest-but-flagged mode.
const legacyUserAgent = "LilDisc (https://github.com/dijama/lildisc)"

// superProperties is the payload of the X-Super-Properties header, and also
// the gateway IDENTIFY properties. Field order matches the official client's
// serialisation, which is why this is a struct and not a map: encoding/json
// sorts map keys alphabetically, and the real client does not.
type superProperties struct {
	OS                string  `json:"os"`
	Browser           string  `json:"browser"`
	ReleaseChannel    string  `json:"release_channel"`
	ClientVersion     string  `json:"client_version"`
	OSVersion         string  `json:"os_version"`
	OSArch            string  `json:"os_arch"`
	AppArch           string  `json:"app_arch"`
	SystemLocale      string  `json:"system_locale"`
	BrowserUserAgent  string  `json:"browser_user_agent"`
	BrowserVersion    string  `json:"browser_version"`
	ClientBuildNumber int     `json:"client_build_number"`
	NativeBuildNumber int     `json:"native_build_number"`
	ClientEventSource *string `json:"client_event_source"`
	HasClientMods     bool    `json:"has_client_mods"`
}

// Identity is the fully resolved client identity. Build it once with Get.
type Identity struct {
	// Enabled reports whether the desktop-client identity is in use. When
	// false, UserAgent is the legacy string and SuperProperties is empty.
	Enabled bool

	UserAgent       string
	SuperProperties string // base64, for X-Super-Properties
	Locale          string // for X-Discord-Locale

	// Properties is the same payload the header carries, decoded, for the
	// gateway IDENTIFY. Sending different values over the socket than over
	// HTTP would be its own anomaly.
	Properties map[string]any
}

// Get returns the process-wide client identity, building it on first use.
//
// It is deliberately lazy rather than computed in an init function, because
// the overrides above arrive from ~/.config/lildisc/env, which main loads
// after package initialisation has already run.
var Get = sync.OnceValue(build)

func build() *Identity {
	if strings.EqualFold(os.Getenv(envDisable), "off") {
		return &Identity{
			Enabled:   false,
			UserAgent: legacyUserAgent,
			Locale:    systemLocale(),
		}
	}

	var (
		client   = envOr(envClientVersion, clientVersion)
		chrome   = envOr(envChromeVersion, chromeVersion)
		electron = envOr(envElectronVersion, electronVersion)
		build    = envInt(envBuildNumber, clientBuildNumber)
		native   = envInt(envNativeBuild, nativeBuildNumber)
		locale   = systemLocale()
	)

	// The exact shape the Electron client's Chromium reports on Linux.
	ua := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) " +
		"discord/" + client +
		" Chrome/" + chrome +
		" Electron/" + electron +
		" Safari/537.36"

	props := superProperties{
		OS:             "Linux",
		Browser:        "Discord Client",
		ReleaseChannel: releaseChannel,
		ClientVersion:  client,
		OSVersion:      kernelVersion(),
		OSArch:         arch(),
		AppArch:        arch(),
		SystemLocale:   locale,
		// The desktop client reports its Electron version here, not the
		// Chrome one. Getting this backwards is a visible inconsistency.
		BrowserUserAgent:  ua,
		BrowserVersion:    electron,
		ClientBuildNumber: build,
		NativeBuildNumber: native,
		ClientEventSource: nil,
		HasClientMods:     false,
	}

	encoded, err := json.Marshal(props)
	if err != nil {
		// Unreachable: superProperties has no unmarshalable field. Fall back
		// to the honest identity rather than sending a half-built one.
		return &Identity{Enabled: false, UserAgent: legacyUserAgent, Locale: locale}
	}

	// Round-trip into a map so the gateway IDENTIFY carries byte-for-byte the
	// same values as the header, without a second hand-maintained literal.
	var asMap map[string]any
	if err := json.Unmarshal(encoded, &asMap); err != nil {
		return &Identity{Enabled: false, UserAgent: legacyUserAgent, Locale: locale}
	}

	return &Identity{
		Enabled:         true,
		UserAgent:       ua,
		SuperProperties: base64.StdEncoding.EncodeToString(encoded),
		Locale:          locale,
		Properties:      asMap,
	}
}

// systemLocale converts the POSIX locale in the environment into the BCP 47
// tag Discord expects, e.g. "en_US.UTF-8" into "en-US". The same value goes
// into X-Discord-Locale and into system_locale, which must agree.
func systemLocale() string {
	raw := ""
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(key); v != "" {
			raw = v
			break
		}
	}

	// Strip the codeset and any modifier: "en_US.UTF-8@euro" -> "en_US".
	raw, _, _ = strings.Cut(raw, ".")
	raw, _, _ = strings.Cut(raw, "@")
	raw = strings.ReplaceAll(raw, "_", "-")

	// "C" and "POSIX" are not locales Discord has ever heard of.
	switch strings.ToLower(raw) {
	case "", "c", "posix":
		return "en-US"
	}
	return raw
}

// kernelVersion returns the running kernel's release, trimmed to its numeric
// head: "7.3.0-rc3-1-MANJARO" becomes "7.3.0".
//
// The official client sends the full release string. Trimming loses nothing
// Discord checks while dropping a distinctive per-machine fingerprint, since
// a distribution suffix and an -rc tag together identify very few machines.
func kernelVersion() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return "6.12.0"
	}

	release := strings.TrimSpace(string(b))
	// Cut at the first character that is neither a digit nor a dot.
	end := strings.IndexFunc(release, func(r rune) bool {
		return !(r >= '0' && r <= '9') && r != '.'
	})
	if end > 0 {
		release = release[:end]
	}
	release = strings.Trim(release, ".")
	if release == "" {
		return "6.12.0"
	}
	return release
}

// arch maps Go's architecture names onto the ones Node reports, which is what
// the Electron client puts in os_arch and app_arch.
func arch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "arm":
		return "arm"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// Header returns the identity headers to attach to a Discord API request.
//
// Use this at every call site that talks to discord.com, including the raw
// net/http ones that bypass arikawa. A request that omits these while its
// neighbours carry them is an anomaly in its own right, so there is one
// source for them and no call site builds its own.
func (i *Identity) Header() http.Header {
	h := http.Header{}
	h.Set("User-Agent", i.UserAgent)
	if i.Locale != "" {
		h.Set("X-Discord-Locale", i.Locale)
	}
	if i.SuperProperties != "" {
		h.Set("X-Super-Properties", i.SuperProperties)
	}
	return h
}

// Apply sets the identity headers on req, leaving everything else alone.
func (i *Identity) Apply(req *http.Request) {
	for key, values := range i.Header() {
		req.Header[key] = values
	}
}
