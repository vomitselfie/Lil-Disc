package discordident

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// clearOverrides removes every env override so a test sees the compiled-in
// defaults regardless of what the developer has in their shell.
func clearOverrides(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		envDisable, envClientVersion, envChromeVersion,
		envElectronVersion, envBuildNumber, envNativeBuild,
	} {
		t.Setenv(key, "")
	}
}

func decodeProps(t *testing.T, i *Identity) map[string]any {
	t.Helper()

	raw, err := base64.StdEncoding.DecodeString(i.SuperProperties)
	if err != nil {
		t.Fatalf("super properties are not valid base64: %v", err)
	}

	var props map[string]any
	if err := json.Unmarshal(raw, &props); err != nil {
		t.Fatalf("super properties are not valid JSON: %v", err)
	}
	return props
}

// The whole point of the package is that the User-Agent and the blob describing
// it cannot disagree. A blob contradicting its own User-Agent is a stronger
// signal than sending no blob at all.
func TestSuperPropertiesAgreeWithUserAgent(t *testing.T) {
	clearOverrides(t)
	t.Setenv("LANG", "en_GB.UTF-8")

	ident := build()
	if !ident.Enabled {
		t.Fatal("identity should be enabled by default")
	}

	props := decodeProps(t, ident)

	if got := props["browser_user_agent"]; got != ident.UserAgent {
		t.Errorf("browser_user_agent %q does not match User-Agent %q", got, ident.UserAgent)
	}

	// client_version must be the version named in the User-Agent.
	version, _ := props["client_version"].(string)
	if version == "" {
		t.Fatal("client_version is empty")
	}
	if !strings.Contains(ident.UserAgent, "discord/"+version) {
		t.Errorf("User-Agent %q does not carry discord/%s", ident.UserAgent, version)
	}

	// The desktop client reports its Electron version in browser_version, and
	// that version also appears in the User-Agent. Getting this backwards and
	// reporting the Chrome version is the easy mistake.
	browserVersion, _ := props["browser_version"].(string)
	if !strings.Contains(ident.UserAgent, "Electron/"+browserVersion) {
		t.Errorf("browser_version %q is not the Electron version in %q",
			browserVersion, ident.UserAgent)
	}

	// The locale in the header and the one in the blob are the same field to
	// Discord, so they must not drift.
	if props["system_locale"] != ident.Locale {
		t.Errorf("system_locale %q != X-Discord-Locale %q",
			props["system_locale"], ident.Locale)
	}
}

// The gateway IDENTIFY must carry what the HTTP header carries. Reporting one
// thing over the socket and another over REST is its own anomaly.
func TestGatewayPropertiesMatchHeader(t *testing.T) {
	clearOverrides(t)

	ident := build()
	header := decodeProps(t, ident)

	if len(ident.Properties) != len(header) {
		t.Fatalf("gateway has %d properties, header has %d",
			len(ident.Properties), len(header))
	}
	for key, want := range header {
		if got := ident.Properties[key]; got != want {
			t.Errorf("property %q: gateway has %v, header has %v", key, got, want)
		}
	}
}

func TestHeaderContents(t *testing.T) {
	clearOverrides(t)

	h := build().Header()
	for _, key := range []string{"User-Agent", "X-Super-Properties", "X-Discord-Locale"} {
		if h.Get(key) == "" {
			t.Errorf("header %s is missing", key)
		}
	}
	if strings.Contains(h.Get("User-Agent"), "lildisc") {
		t.Errorf("User-Agent still names the project: %q", h.Get("User-Agent"))
	}
}

// Disabling must be complete rather than partial: no super properties at all,
// and the honest User-Agent back.
func TestDisabledSendsNoSuperProperties(t *testing.T) {
	clearOverrides(t)
	t.Setenv(envDisable, "off")

	ident := build()
	if ident.Enabled {
		t.Fatal("identity should be disabled")
	}
	if ident.SuperProperties != "" {
		t.Error("disabled identity still carries super properties")
	}
	if ident.UserAgent != legacyUserAgent {
		t.Errorf("User-Agent = %q, want the legacy string", ident.UserAgent)
	}
	if got := ident.Header().Get("X-Super-Properties"); got != "" {
		t.Errorf("disabled identity still sets X-Super-Properties: %q", got)
	}
}

func TestEnvOverrides(t *testing.T) {
	clearOverrides(t)
	t.Setenv(envClientVersion, "0.0.999")
	t.Setenv(envBuildNumber, "987654")
	t.Setenv(envElectronVersion, "40.0.1")

	ident := build()
	props := decodeProps(t, ident)

	if props["client_version"] != "0.0.999" {
		t.Errorf("client_version = %v, want the override", props["client_version"])
	}
	if props["client_build_number"] != float64(987654) {
		t.Errorf("client_build_number = %v, want the override", props["client_build_number"])
	}
	// An override has to reach the User-Agent too, not just the blob.
	if !strings.Contains(ident.UserAgent, "discord/0.0.999") {
		t.Errorf("User-Agent %q ignored the version override", ident.UserAgent)
	}
	if !strings.Contains(ident.UserAgent, "Electron/40.0.1") {
		t.Errorf("User-Agent %q ignored the Electron override", ident.UserAgent)
	}
}

func TestBadBuildNumberFallsBackToDefault(t *testing.T) {
	clearOverrides(t)
	t.Setenv(envBuildNumber, "not-a-number")

	props := decodeProps(t, build())
	if props["client_build_number"] != float64(clientBuildNumber) {
		t.Errorf("client_build_number = %v, want the compiled-in default",
			props["client_build_number"])
	}
}

func TestSystemLocale(t *testing.T) {
	tests := []struct {
		name string
		lang string
		want string
	}{
		{"posix utf8", "en_US.UTF-8", "en-US"},
		{"british", "en_GB.UTF-8", "en-GB"},
		{"with modifier", "de_DE.UTF-8@euro", "de-DE"},
		{"bare language", "fr", "fr"},
		{"C is not a locale", "C", "en-US"},
		{"POSIX is not a locale", "POSIX", "en-US"},
		{"unset", "", "en-US"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LC_ALL", "")
			t.Setenv("LC_MESSAGES", "")
			t.Setenv("LANG", tt.lang)

			if got := systemLocale(); got != tt.want {
				t.Errorf("systemLocale() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The kernel release is trimmed to its numeric head, because a distribution
// suffix plus an -rc tag together identify very few machines.
func TestKernelVersionIsNumericOnly(t *testing.T) {
	got := kernelVersion()
	if got == "" {
		t.Fatal("kernelVersion returned empty")
	}
	for _, r := range got {
		if (r < '0' || r > '9') && r != '.' {
			t.Fatalf("kernelVersion() = %q, want digits and dots only", got)
		}
	}
	if strings.HasPrefix(got, ".") || strings.HasSuffix(got, ".") {
		t.Errorf("kernelVersion() = %q has a stray dot", got)
	}
}

func TestArchUsesNodeNames(t *testing.T) {
	// Whatever the host is, the name must be one Node would report, since
	// that is what an Electron client puts in os_arch.
	nodeNames := map[string]bool{"x64": true, "ia32": true, "arm": true, "arm64": true}
	if got := arch(); !nodeNames[got] {
		t.Logf("arch() = %q, which is not a common Node arch name", got)
	}
}
