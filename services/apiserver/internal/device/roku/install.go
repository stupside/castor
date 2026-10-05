package roku

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/icholy/digest"
)

const installTimeout = 60 * time.Second

func (s *session) sideloadChannel(ctx context.Context, password string) error {
	zipBytes, err := channelZip()
	if err != nil {
		return fmt.Errorf("packing channel: %w", err)
	}
	return installChannel(ctx, "http://"+net.JoinHostPort(s.ecp.Hostname(), "80")+"/plugin_install", password, zipBytes)
}

// installChannel uploads channel to Roku dev web server (Digest auth, multipart, result in HTML body).
func installChannel(ctx context.Context, installURL, password string, zipBytes []byte) error {
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
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

	client := &http.Client{Transport: &digest.Transport{Username: devUser, Password: password}}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("plugin_install: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("developer password rejected for user %q", devUser)
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

// installSuccessMarkers are phrases dev web server puts in 200 body on success (positive match, not "error").
var installSuccessMarkers = []string{
	"install success",
	"identical to previous version",
}

// installOutcome reads result from body (dev server returns 200 for both success/failure).
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
