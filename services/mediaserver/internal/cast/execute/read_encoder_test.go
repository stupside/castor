package execute

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/encoder"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

func TestAReadWhoseHardwareEncodeExitedIsTakenForTheEncodesFailure(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	origin := serveFixture(t, ffmpegPath)
	gone := httptest.NewServer(http.HandlerFunc(http.NotFound))
	t.Cleanup(gone.Close)
	deadURL, _ := url.Parse(gone.URL + "/gone.mp4")

	c := realCast(&fakeDevice{}, ffmpegPath, ffprobePath)
	working := c.Encoders
	c.Encoders = func(ctx context.Context, codec media.Codec, software bool) (encoder.Encoder, bool) {
		if software || codec != media.CodecH264 {
			return working(ctx, codec, true)
		}
		// libx264 refuses 4:4:4 in a baseline profile as it opens, as a phone's encoder refuses a picture it cannot make.
		return encoder.Encoder{Name: "libx264", Codec: media.CodecH264, Hardware: true, Flags: []string{"-profile:v", "baseline", "-pix_fmt", "yuv444p"}}, true
	}

	for _, tt := range []struct {
		name     string
		stream   *source.Stream
		software bool
		blamed   bool
		whole    bool
	}{
		{"a hardware encode that refuses the picture", origin.stream(), false, true, false},
		{"the same read made in software", origin.stream(), true, false, true},
		{"a hardware read of a dead link, which cannot be told from it", &source.Stream{URL: deadURL, ContentType: media.MP4}, false, true, false},
		{"a software read of a dead link", &source.Stream{URL: deadURL, ContentType: media.MP4}, true, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			program := programFromStream(t, tt.stream)
			s := readingAttempt(t, c, recovery.Attempt{Program: program, Fetch: sourceFetchPlan(t, program, testReadDeadline), Decode: media.Axes{Video: true}, Software: tt.software})
			buf, err := s.read(t.TempDir(), program)
			if err != nil {
				t.Fatal(err)
			}
			<-buf.reader.Done()
			e := evidence(ran{reader: buf.reader}, buf.reader.Err(), false)
			if blamed := e.EncodeErr != nil && e.Hardware; blamed != tt.blamed {
				t.Errorf("the read ended on %v with encode error %v (hardware %t), want its encoder blamed %t", e.ReadErr, e.EncodeErr, e.Hardware, tt.blamed)
			}
			if whole := e.ReadErr == nil; whole != tt.whole {
				t.Errorf("the read ended on %v, want it whole %t", e.ReadErr, tt.whole)
			}
		})
	}
}
