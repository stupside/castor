package execute

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

func TestAHardwareEncoderThatCrashesOrHangsIsKeptOutOfLaterCasts(t *testing.T) {
	t.Parallel()
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	faulty, err := filepath.Abs("testdata/ffmpeg-fault")
	if err != nil {
		t.Fatal(err)
	}
	// MPEG-4 forces an encode; noise makes it outweigh the socket buffers.
	stream := sourceOf(t, generateFixture(t, ffmpegPath, "movie.ts", 60, []string{"-vf", "noise=alls=40:allf=t", "-c:v", "mpeg4", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"}))
	software := ffmpeg.Encoders(ffmpegPath, nil)

	for _, tt := range []struct {
		name    string
		encoder string
		slow    bool
		retired bool
	}{
		{"one that crashes while playing", "h264_crash", true, true},
		{"one that hangs while playing", "h264_hang", true, true},
		// A picture one encoder refuses says nothing of the next cast's.
		{"one that will not start", "h264_bogus", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var r Retirement
			c := realCast(&fetchingDevice{caps: dlnaLike(), slow: tt.slow}, faulty, ffprobePath)
			c.Encoders = r.Encoders(func(ctx context.Context, codec media.Codec, soft bool) (ffmpeg.Encoder, bool) {
				if soft || codec != media.CodecH264 {
					return software(ctx, codec, true)
				}
				return ffmpeg.Encoder{Name: tt.encoder, Codec: media.CodecH264, Hardware: true, Flags: []string{"-pix_fmt", "yuv420p"}}, true
			})
			ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
			defer cancel()
			intent := recovery.Intent{Candidates: []*source.Stream{stream}, Deadline: testReadDeadline, Turns: &revisions{}}
			if err := recovery.Cast(ctx, intent, r.Runner(runnerFunc(c.Run)), linkOrigin{t}); err != nil {
				t.Fatalf("the cast failed instead of going on in software: %v", err)
			}
			next, ok := c.Encoders(t.Context(), media.CodecH264, false)
			if !ok || next.Hardware == tt.retired {
				t.Errorf("the next cast encodes with %q (hardware %t), want hardware kept out %t", next.Name, next.Hardware, tt.retired)
			}
		})
	}
}
