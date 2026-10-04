package ffmpeg

import (
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// The command-line fragments ffmpeg and ffprobe both open a source with, so the probe opens it as the reader will.

// HLSTolerances are the HLS demuxer's options castor reads every playlist with.
func HLSTolerances(segmentRetries int) []string {
	args := []string{
		// A publisher's EXT-X-START decides where a live read joins, not the demuxer's default three segments back.
		"-prefer_x_start", "1",
		"-allowed_extensions", "ALL",
		"-allowed_segment_extensions", "ALL",
		"-extension_picky", "0",
		"-seg_format_options", "extension_picky=0",
	}
	// Only the HLS demuxer takes it; others abort on the unknown option.
	if segmentRetries > 0 {
		args = append(args, "-seg_max_retry", strconv.Itoa(segmentRetries))
	}
	return args
}

// HeaderArgs renders h as the ffmpeg/ffprobe -headers flag pair, or nil when h is empty.
func HeaderArgs(h http.Header) []string {
	if len(h) == 0 {
		return nil
	}
	var b strings.Builder
	for _, key := range slices.Sorted(maps.Keys(h)) {
		for _, v := range h[key] {
			b.WriteString(key)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\r\n")
		}
	}
	return []string{"-headers", b.String()}
}
