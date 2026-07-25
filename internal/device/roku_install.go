package device

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/icholy/digest"

	"github.com/stupside/castor/internal/device/rokuchannel"
)

const rokuInstallTimeout = 60 * time.Second

func (r *rokuDevice) sideloadChannel(ctx context.Context, user, password string) error {
	zipBytes, err := rokuchannel.Zip()
	if err != nil {
		return fmt.Errorf("packing channel: %w", err)
	}
	return installChannel(ctx, "http://"+r.ecp.Hostname()+"/plugin_install", user, password, zipBytes)
}

// installChannel uploads the channel to a Roku developer web server. That server
// is on port 80, speaks HTTP Digest auth, takes a multipart archive, and reports
// the outcome in the HTML body.
func installChannel(ctx context.Context, installURL, user, password string, zipBytes []byte) error {
	ctx, cancel := context.WithTimeout(ctx, rokuInstallTimeout)
	defer cancel()

	body, contentType, err := installBody(zipBytes)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, installURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)

	client := &http.Client{Transport: &digest.Transport{Username: user, Password: password}}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("plugin_install: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("developer password rejected for user %q", user)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("plugin_install: %s", resp.Status)
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return installOutcome(page)
}

func installBody(zipBytes []byte) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("mysubmit", "Install"); err != nil {
		return nil, "", err
	}
	part, err := w.CreateFormFile("archive", "castor.zip")
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(zipBytes); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

// installSuccessMarkers are the phrases the Roku dev web server puts in its 200
// response body on a successful sideload (a fresh install, or an unchanged
// re-upload). Matching these positively is more robust than scanning for
// "error"/"failure": the outcome page is a full HTML UI whose boilerplate can
// carry those words, and a real failure need not.
var installSuccessMarkers = []string{
	"install success",
	"identical to previous version",
}

// installOutcome reads the install result out of the response body: the dev
// server returns 200 for both success and failure and signals which in the text.
// Anything without a known success marker is treated as a failure, surfacing a
// trimmed snippet of the page so an unexpected response is diagnosable.
func installOutcome(page []byte) error {
	text := strings.ToLower(string(page))
	for _, marker := range installSuccessMarkers {
		if strings.Contains(text, marker) {
			return nil
		}
	}
	snippet := strings.TrimSpace(string(page))
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	return fmt.Errorf("roku rejected the channel: %s", snippet)
}
