package mods

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotkit/app/prefs"
)

// MediaHostPolicy is what happens to a file too large for Discord.
type MediaHostPolicy string

const (
	// MediaHostAsk asks before each upload, offering to make it Always.
	MediaHostAsk MediaHostPolicy = "Ask"
	// MediaHostAlways uploads without asking.
	MediaHostAlways MediaHostPolicy = "Always"
	// MediaHostNever drops the file.
	MediaHostNever MediaHostPolicy = "Never"
)

// Uploading a file the user tried to send to Discord to a public,
// anonymous host instead is a different act from sending it to Discord, so
// it asks first by default. This replaces a boolean that was on by default
// and uploaded silently. It has a new name so the old saved "on" is not
// read as consent.
var mediaHostPolicy = prefs.NewEnumList(MediaHostAsk, prefs.EnumListMeta[MediaHostPolicy]{
	PropMeta: prefs.PropMeta{
		Name:    "Upload Oversized Files to 0x0.st",
		Section: "Mods",
		Description: "When a file is larger than Discord's upload limit, it can be " +
			"uploaded to 0x0.st, a public anonymous file host, and its link " +
			"pasted into the message instead. Anyone with the link can " +
			"download the file.",
	},
	Options: []MediaHostPolicy{MediaHostAsk, MediaHostAlways, MediaHostNever},
})

// MediaHostPolicyValue returns the current policy for oversized files.
func MediaHostPolicyValue() MediaHostPolicy { return mediaHostPolicy.Value() }

// SetMediaHostPolicy changes the policy and saves preferences, for the
// "Always" answer to the consent prompt.
func SetMediaHostPolicy(ctx context.Context, p MediaHostPolicy) {
	mediaHostPolicy.Publish(p)
	snapshot := prefs.TakeSnapshot()
	go func() {
		if err := snapshot.Save(ctx); err != nil {
			slog.Warn("mediahost: cannot save preferences", "err", err)
		}
	}()
}

// MediaHostName is the host oversized files go to, for user-facing text.
const MediaHostName = "0x0.st"

var freeUploadLimitMiB = prefs.NewInt(20, prefs.IntMeta{
	Name:    "Free Upload Limit (MiB)",
	Section: "Mods",
	Description: "The attachment size Discord accepts without Nitro. Only " +
		"raises the limit — Nitro and server-boost tiers are detected " +
		"separately and are already higher. Files above this are sent to the " +
		"external media host instead. Lower it if uploads start being " +
		"rejected; Discord has changed this figure several times.",
	Min: 1,
	Max: 500,
})

// FreeUploadLimit is the configured free-tier attachment ceiling, in bytes.
//
// This exists because arikawa compiles Discord's free-tier figure in as a
// constant, so a stale dependency silently diverts files to a third-party
// host that Discord would have taken. Being a preference means a future
// change on Discord's side is a setting, not a release.
func FreeUploadLimit() int64 {
	return int64(freeUploadLimitMiB.Value()) << 20
}

// mediaHostClient is a separate http.Client with a generous timeout because
// the shared httpClient (10s) is too short for multi-megabyte uploads.
// Cancellation goes through the request context.
var mediaHostClient = &http.Client{Timeout: 5 * time.Minute}

const (
	zeroXMaxSize = 512 << 20 // 512 MiB

	// hostMaxResponseSize bounds how much of the host's response we'll read.
	// 0x0.st returns a single URL on one line; 1 KiB is several times what
	// any legitimate response will be.
	hostMaxResponseSize = 1024
)

// MediaUploadRequest describes a single file to push to an external host.
// Open is called once when the upload starts; the returned reader is fully
// drained, then closed.
type MediaUploadRequest struct {
	Name     string
	MIMEType string
	Size     int64
	Open     func() (io.ReadCloser, error)
}

// UploadToMediaHost uploads the request to an external file host on a
// background goroutine and invokes onResult on the GTK main thread when
// the upload terminates. On success, url is set and err is nil; on
// failure, url is empty and err is non-nil.
//
// Uploads go to 0x0.st (size-based retention, 512 MiB limit): anonymous, no
// API key, no fingerprinting beyond a plain multipart POST. It is the only
// host, so a failure here is terminal rather than a fallback. The caller is
// responsible for having the user's consent; see MediaHostPolicyValue.
func UploadToMediaHost(ctx context.Context, req MediaUploadRequest, onResult func(url string, err error)) {
	if mediaHostPolicy.Value() == MediaHostNever {
		glib.IdleAdd(func() { onResult("", errors.New("uploading oversized files is turned off in preferences")) })
		return
	}
	if req.Size <= 0 {
		glib.IdleAdd(func() { onResult("", errors.New("file is empty")) })
		return
	}

	go func() {
		url, err := uploadToHost(ctx, req)
		// Always marshal the result back to the GTK main thread; consumers
		// touch widgets in the callback.
		glib.IdleAdd(func() { onResult(url, err) })
	}()
}

func uploadToHost(ctx context.Context, req MediaUploadRequest) (string, error) {
	if req.Size > zeroXMaxSize {
		return "", fmt.Errorf(
			"file is %d bytes, over the %d byte host limit", req.Size, zeroXMaxSize)
	}

	slog.Info("mediahost: uploading to 0x0.st", "name", req.Name, "size", req.Size)
	url, err := uploadToZeroX(ctx, req)
	if err != nil {
		slog.Warn("mediahost: 0x0.st upload failed", "name", req.Name, "err", err)
		return "", fmt.Errorf("media host upload failed: %w", err)
	}
	slog.Info("mediahost: 0x0.st upload succeeded", "name", req.Name, "url", url)
	return url, nil
}

// uploadToZeroX does a multipart POST to 0x0.st.
//
// API: POST https://0x0.st
//
//	file=<binary>
//
// Returns the URL on success as the entire response body.
func uploadToZeroX(ctx context.Context, req MediaUploadRequest) (string, error) {
	return doMultipartUpload(ctx, "https://0x0.st", "file", req)
}

// doMultipartUpload streams the file via an io.Pipe so we never buffer the
// whole payload in memory — important for the gigabyte case.
func doMultipartUpload(ctx context.Context, endpoint, fileFieldName string, req MediaUploadRequest) (string, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		// Any error from this goroutine is propagated through the pipe.
		writeErr := func(err error) {
			pw.CloseWithError(err)
		}

		rc, err := req.Open()
		if err != nil {
			writeErr(fmt.Errorf("open file: %w", err))
			return
		}
		defer rc.Close()

		fw, err := mw.CreateFormFile(fileFieldName, req.Name)
		if err != nil {
			writeErr(fmt.Errorf("create form file: %w", err))
			return
		}
		if _, err := io.Copy(fw, rc); err != nil {
			writeErr(fmt.Errorf("copy file body: %w", err))
			return
		}
		if err := mw.Close(); err != nil {
			writeErr(fmt.Errorf("close multipart writer: %w", err))
			return
		}
		pw.Close()
	}()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, pr)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := mediaHostClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, hostMaxResponseSize+1))
	bodyStr := strings.TrimSpace(string(body))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("host returned HTTP %d: %s", resp.StatusCode, bodyStr)
	}
	if !strings.HasPrefix(bodyStr, "https://") {
		return "", fmt.Errorf("host returned unexpected body: %q", bodyStr)
	}
	return bodyStr, nil
}
