//go:build tosfeatures

package mods

// When built with -tags=tosfeatures, all ToS-relevant features are enabled
// by default. The user can still toggle them off in preferences.
//
// Build without this tag for a distribution-safe default where these
// features require explicit opt-in.
//
// Usage:
//   go build -tags=tosfeatures -v .
func init() {
	enableFakeNitro.Publish(true)
	enableHiddenChannels.Publish(true)
	enableRandomFilenames.Publish(true)
	enableStripMetadata.Publish(true)
}
