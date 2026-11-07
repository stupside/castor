// Package hls implements bounded HLS publication and generated subtitle renditions.
package hls

import (
	"strconv"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage/diskbudget"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

type Adapter struct{}

func (Adapter) Select(f media.FormatSupport, c *output.Captions, runtime time.Duration) (output.Plan, bool) {
	switch f.Transport {
	case media.TransportHLS, media.TransportHLSWebVTT:
		if c != nil && (f.Transport != media.TransportHLSWebVTT || c.Cues == nil || c.VideoOffset < 0 || c.VideoOffset >= CaptionSegmentDuration) {
			return nil, false
		}
		return hlsPlan{captions: c, runtime: runtime}, true
	}
	return nil, false
}

type hlsPlan struct {
	captions *output.Captions
	runtime  time.Duration
}

// Retained event playlists keep the program's clock and earlier fragments while
// a finite download grows. Rolling live and generated captions keep their window.
func (p hlsPlan) retained() bool { return p.runtime > 0 && p.captions == nil }

func format() container.Format {
	return container.Format{
		ContentType: media.HLS, Extension: ".m3u8", Muxer: ffmpeg.FormatHLS, Delivery: container.DeliverSegmented, Framing: media.FramingOutOfBand,
		Tuning: container.Tuning{Args: []string{
			"-hls_time", strconv.FormatFloat(CaptionSegmentDuration.Seconds(), 'f', -1, 64),
			"-hls_list_size", strconv.Itoa(captionWindowSegments),
			"-hls_flags", "delete_segments+independent_segments",
			"-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", "init.mp4",
			"-hls_segment_filename", "seg_%05d.m4s",
		}, Output: "stream.m3u8"},
	}
}
func (p hlsPlan) Format() container.Format {
	f := format()
	if p.retained() {
		f.Tuning.Args = []string{
			"-hls_time", strconv.FormatFloat(CaptionSegmentDuration.Seconds(), 'f', -1, 64),
			"-hls_list_size", "0", "-hls_playlist_type", "event",
			"-hls_flags", "independent_segments+temp_file",
			"-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4",
			"-master_pl_name", "master.m3u8",
			"-hls_segment_filename", "seg_%05d.m4s",
		}
	}
	return f
}
func (p hlsPlan) EncoderFormat() container.Format {
	if p.captions == nil {
		return p.Format()
	}
	f, _ := container.For(media.MP4)
	f.Tuning.MovFlags = []string{"delay_moov", "default_base_moof"}
	micros := strconv.FormatInt(CaptionSegmentDuration.Microseconds(), 10)
	f.Tuning.Args = []string{"-frag_duration", micros, "-min_frag_duration", micros}
	return f
}

func (hlsPlan) Splices() bool { return false }

func (p hlsPlan) Pace(burn bool) fetch.Pace {
	if p.retained() && !burn {
		return fetch.Pace{}
	}
	// Every rolling rendition stays at receiver speed, including burn-in.
	if p.captions != nil || burn {
		return fetch.Pace{Realtime: 1, Burst: fetch.EncodeBurst}
	}
	return fetch.Pace{Realtime: 1, Burst: CaptionSegmentDuration * captionWindowSegments}
}
func (p hlsPlan) Reserve(dir string) (func() error, error) {
	if p.retained() {
		return reserveDirectory(dir, storage.DefaultSpoolLimit, diskbudget.Shared)
	}
	return Reserve(dir)
}
func (p hlsPlan) Prepare(o output.Opening, dir string) (output.Prepared, error) {
	o.Format = p.Format()
	srv, err := newSegments(o, dir, p.captions)
	if err != nil {
		return output.Prepared{}, err
	}
	if p.retained() {
		srv.runtime = p.runtime
	}
	return output.Prepared{Output: srv, Write: srv.Write, WatchStorage: srv.WatchStorage}, nil
}
