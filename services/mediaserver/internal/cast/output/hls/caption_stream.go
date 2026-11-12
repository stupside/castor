package hls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/Eyevinn/hls-m3u8/m3u8"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage/diskbudget"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// CaptionSegmentDuration is the fixed native fragment duration. The first
// fragment must contain video to establish its mapping to the transcript clock.
const CaptionSegmentDuration = 4 * time.Second

const (
	captionWindowSegments = 8
	captionTargetDuration = 6
)

type captionFragment struct {
	media           m3u8.MediaSegment
	longestPlaylist float64
}

type retiredCaptionFragment struct {
	name    string
	expires time.Time
}

// publishCaptionHLS consumes the encoder's fragmented MP4 stdout. It reads only
// one complete fragment ahead of the transcript, then stops draining the pipe.
// This backpressures FFmpeg itself, rather than letting a rolling video window
// delete segments whose subtitles have not settled yet.
func publishCaptionHLS(ctx context.Context, root *os.Root, producer io.Reader, cues *subtitle.Cues, videoOffset time.Duration) error {
	defer root.Remove("fragment.tmp")
	defer root.Remove("init.tmp")
	defer root.Remove("playlist.tmp")

	init, err := root.OpenFile("init.tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	for _, name := range []string{"ftyp", "moov"} {
		header, err := mp4.DecodeHeader(producer)
		if err == nil && header.Name != name {
			err = fmt.Errorf("expected %s, received %s", name, header.Name)
		}
		if err == nil {
			err = copyCaptionBox(root, init, producer, header)
		}
		if err != nil {
			init.Close()
			return fmt.Errorf("reading caption media initialization: %w", err)
		}
	}
	if err := init.Close(); err != nil {
		return err
	}
	if err := root.Rename("init.tmp", initSection); err != nil {
		return err
	}
	var playlist *m3u8.MediaPlaylist
	var segments []captionFragment
	var retired []retiredCaptionFragment
	var origin float64
	for sequence := uint64(0); ; sequence++ {
		retired, err = expireCaptionFragments(root, retired, time.Now())
		if err != nil {
			return err
		}
		header, err := mp4.DecodeHeader(producer)
		if err == nil && header.Name == "mfra" {
			// FFmpeg's optional final random-access index is not part of any segment.
			if header.Size > uint64(SegmentDirectoryLimit) {
				return diskbudget.ErrLimit
			}
			if _, err = io.CopyN(io.Discard, producer, int64(header.Size)-int64(header.Hdrlen)); err != nil {
				return err
			}
			header, err = mp4.DecodeHeader(producer)
		}
		if errors.Is(err, io.EOF) {
			if sequence == 0 {
				return fmt.Errorf("caption media ended before its first fragment")
			}
			playlist.Close()
			return writeCaptionPlaylist(root, playlist)
		}
		if err != nil {
			return fmt.Errorf("reading caption fragment: %w", err)
		}
		if header.Name != "moof" {
			return fmt.Errorf("expected moof, received %s", header.Name)
		}
		fragment, err := root.OpenFile("fragment.tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = copyCaptionBox(root, fragment, producer, header)
		if err == nil {
			header, err = mp4.DecodeHeader(producer)
			if err == nil && header.Name != "mdat" {
				err = fmt.Errorf("expected mdat, received %s", header.Name)
			}
			if err == nil {
				err = copyCaptionBox(root, fragment, producer, header)
			}
		}
		if err = errors.Join(err, fragment.Close()); err != nil {
			return err
		}
		timing, err := captionMedia(root, initSection, "fragment.tmp")
		if err != nil {
			return err
		}
		if sequence == 0 {
			if timing.VideoStart == nil {
				return fmt.Errorf("first caption fragment has no video clock")
			}
			origin = *timing.VideoStart - videoOffset.Seconds()
		}
		duration := timing.End - timing.Start
		if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			return fmt.Errorf("caption fragment has invalid duration")
		}
		if math.Round(duration) > captionTargetDuration {
			return fmt.Errorf("caption fragment duration %.3fs exceeds stable HLS target %ds", duration, captionTargetDuration)
		}
		ticker := time.NewTicker(50 * time.Millisecond)
		for {
			snapshot := cues.Snapshot(0, 0)
			if snapshot.Complete || snapshot.SettledTo+0.000001 >= timing.End-origin {
				break
			}
			select {
			case <-ctx.Done():
				ticker.Stop()
				return ctx.Err()
			case <-ticker.C:
			}
		}
		ticker.Stop()
		if err := ctx.Err(); err != nil {
			return err
		}
		name := fmt.Sprintf(segmentNames, sequence)
		if err := root.Rename("fragment.tmp", name); err != nil {
			return err
		}
		segments = append(segments, captionFragment{media: m3u8.MediaSegment{URI: name, Duration: duration, SeqId: sequence}})
		if len(segments) > captionWindowSegments {
			removed := segments[0]
			retired = append(retired, retiredCaptionFragment{name: removed.media.URI, expires: time.Now().Add(time.Duration((removed.media.Duration + removed.longestPlaylist) * float64(time.Second)))})
			segments = segments[1:]
		}
		var windowDuration float64
		for _, segment := range segments {
			windowDuration += segment.media.Duration
		}
		for i := range segments {
			segments[i].longestPlaylist = max(segments[i].longestPlaylist, windowDuration)
		}
		// Build the small current window directly: the upstream writer encodes
		// contiguous segments and does not wrap its ring when serializing.
		playlist, err = m3u8.NewMediaPlaylist(captionWindowSegments, captionWindowSegments)
		if err != nil {
			return err
		}
		playlist.SeqNo = segments[0].media.SeqId
		playlist.SetDefaultMap(initSection, 0, 0)
		playlist.SetVersion(7)
		playlist.SetTargetDuration(captionTargetDuration)
		for _, segment := range segments {
			if err := playlist.Append(segment.media.URI, segment.media.Duration, ""); err != nil {
				return err
			}
		}
		if err := writeCaptionPlaylist(root, playlist); err != nil {
			return err
		}
	}
}

func copyCaptionBox(root *os.Root, dst io.Writer, src io.Reader, header mp4.BoxHeader) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(segmentFileLimit + 1)
	dir.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > segmentFileLimit {
		return diskbudget.ErrLimit
	}
	remaining := SegmentDirectoryLimit
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected caption output %q", entry.Name())
		}
		remaining -= info.Size()
	}
	if remaining < 0 || header.Size > uint64(remaining) {
		return diskbudget.ErrLimit
	}
	if err := mp4.EncodeHeaderWithSize(header.Name, header.Size, header.Hdrlen == 16, dst); err != nil {
		return err
	}
	_, err = io.CopyN(dst, src, int64(header.Size)-int64(header.Hdrlen))
	return err
}

func writeCaptionPlaylist(root *os.Root, playlist *m3u8.MediaPlaylist) error {
	file, err := root.OpenFile("playlist.tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(playlist.Encode().Bytes())
	if err = errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return root.Rename("playlist.tmp", "stream.m3u8")
}

// RFC 8216 section 6.2.2 retains a removed fragment for its duration plus the
// longest playlist in which it appeared. Expiry happens before reading another
// fragment; directory quotas still bound unusually fast or malformed producers.
func expireCaptionFragments(root *os.Root, fragments []retiredCaptionFragment, now time.Time) ([]retiredCaptionFragment, error) {
	retained := fragments[:0]
	for _, fragment := range fragments {
		if now.Before(fragment.expires) {
			retained = append(retained, fragment)
			continue
		}
		if err := root.Remove(fragment.name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return retained, nil
}
