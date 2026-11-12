package hls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

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

// captionFragment is one video fragment of the window, named beside the captions that play with it.
type captionFragment struct {
	media, captions string
	sequence        uint64
	start, duration float64
	longestPlaylist float64
}

type retiredCaptionFragment struct {
	media, captions string
	expires         time.Time
}

// write consumes the encoder's fragmented MP4 stdout. It reads only
// one complete fragment ahead of the transcript, then stops draining the pipe.
// This backpressures FFmpeg itself, rather than letting a rolling video window
// delete segments whose subtitles have not settled yet.
func (c *captionDelivery) write(ctx context.Context, producer io.Reader) error {
	root := c.root
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
	var segments []captionFragment
	var retired []retiredCaptionFragment
	var origin float64
	var codecs string
	var bandwidth uint64
	for sequence := uint64(0); ; sequence++ {
		retired, err = c.expire(retired, time.Now())
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
			return c.publish(segments, true, codecs, bandwidth)
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
			origin = *timing.VideoStart - c.videoOffset.Seconds()
		}
		duration := timing.End - timing.Start
		if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			return fmt.Errorf("caption fragment has invalid duration")
		}
		if math.Round(duration) > captionTargetDuration {
			return fmt.Errorf("caption fragment duration %.3fs exceeds stable HLS target %ds", duration, captionTargetDuration)
		}
		// The fragment's captions are frozen the moment the transcript settles past its end.
		var snapshot subtitle.Snapshot
		ticker := time.NewTicker(50 * time.Millisecond)
		for {
			snapshot = c.cues.Snapshot(timing.Start-origin, timing.End-origin)
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
		info, err := root.Stat(name)
		if err != nil {
			return err
		}
		settled := captionFragment{media: name, captions: fmt.Sprintf(captionNames, sequence), sequence: sequence, start: timing.Start, duration: duration}
		vtt := captionVTT(snapshot.Cues, origin)
		c.mu.Lock()
		c.vtt[settled.captions] = vtt
		c.mu.Unlock()
		codecs = timing.Codecs
		bandwidth = max(bandwidth, uint64(math.Ceil(float64(info.Size()+int64(len(vtt)))*8/duration)))
		segments = append(segments, settled)
		if len(segments) > captionWindowSegments {
			removed := segments[0]
			retired = append(retired, retiredCaptionFragment{media: removed.media, captions: removed.captions, expires: time.Now().Add(time.Duration((removed.duration + removed.longestPlaylist) * float64(time.Second)))})
			segments = segments[1:]
			c.cues.DiscardBefore(segments[0].start - origin)
		}
		var windowDuration float64
		for _, segment := range segments {
			windowDuration += segment.duration
		}
		for i := range segments {
			segments[i].longestPlaylist = max(segments[i].longestPlaylist, windowDuration)
		}
		if err := c.publish(segments, false, codecs, bandwidth); err != nil {
			return err
		}
	}
}

// publish renders the window's video and caption playlists in step; the video one lands on disk, served and probed like any castor playlist.
func (c *captionDelivery) publish(segments []captionFragment, closed bool, codecs string, bandwidth uint64) error {
	var video, captions strings.Builder
	fmt.Fprintf(&video, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-MAP:URI=\"%s\"\n", captionTargetDuration, segments[0].sequence, initSection)
	fmt.Fprintf(&captions, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n", captionTargetDuration, segments[0].sequence)
	for _, segment := range segments {
		fmt.Fprintf(&video, "#EXTINF:%.6f,\n%s\n", segment.duration, segment.media)
		fmt.Fprintf(&captions, "#EXTINF:%.6f,\n%s\n", segment.duration, segment.captions)
	}
	if closed {
		video.WriteString("#EXT-X-ENDLIST\n")
		captions.WriteString("#EXT-X-ENDLIST\n")
	}
	if err := writeCaptionPlaylist(c.root, c.playlist, video.String()); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.master = fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"captions\",NAME=\"Transcription\",DEFAULT=YES,AUTOSELECT=YES,FORCED=NO,URI=\"%s\"\n#EXT-X-STREAM-INF:BANDWIDTH=%d,CODECS=\"%s\",SUBTITLES=\"captions\"\n%s\n", captionPlaylist, bandwidth, codecs, c.playlist)
	c.captions = captions.String()
	return nil
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

func writeCaptionPlaylist(root *os.Root, name, playlist string) error {
	file, err := root.OpenFile("playlist.tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, playlist)
	if err = errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return root.Rename("playlist.tmp", name)
}

// RFC 8216 section 6.2.2 retains a removed fragment for its duration plus the
// longest playlist in which it appeared. Expiry happens before reading another
// fragment; directory quotas still bound unusually fast or malformed producers.
func (c *captionDelivery) expire(fragments []retiredCaptionFragment, now time.Time) ([]retiredCaptionFragment, error) {
	retained := fragments[:0]
	for _, fragment := range fragments {
		if now.Before(fragment.expires) {
			retained = append(retained, fragment)
			continue
		}
		if err := c.root.Remove(fragment.media); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		c.mu.Lock()
		delete(c.vtt, fragment.captions)
		c.mu.Unlock()
	}
	return retained, nil
}
