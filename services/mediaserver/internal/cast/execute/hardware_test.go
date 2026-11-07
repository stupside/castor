package execute

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

type fixedOrigin struct{ program media.Program }

func (o fixedOrigin) Resolve(context.Context, *source.Stream, source.Rendition, media.Bitrate) (source.Resolution, error) {
	return source.Resolution{Program: o.program.Clone()}, nil
}

type revisions struct{ actions []recovery.Action }

func (*revisions) Attempting(int) {}
func (r *revisions) Revising(action recovery.Action, _ string) {
	r.actions = append(r.actions, action)
}

func TestAHardwareEncoderThatFailsBeforeItsFirstOutputIsReplacedBySoftware(t *testing.T) {
	ffmpegPath, ffprobePath := requireFFmpegTools(t)
	// MPEG-4 Part 2 is a picture the H.264-only renderer cannot take, so it is encoded.
	file := generateFixture(t, ffmpegPath, "movie.ts", 2, []string{"-c:v", "mpeg4", "-c:a", "aac", "-ac", "2", "-shortest", "-f", "mpegts"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, file) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL + "/movie.ts")
	stream := &source.Stream{URL: u, ContentType: media.MPEGTS}

	dev := &fakeDevice{caps: dlnaLike(), drain: true}
	cast := realCast(dev, ffmpegPath, ffprobePath)
	catalog, err := ffmpeg.NewCatalog(ffmpegPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	software := catalog.Software
	var asked, retired []string
	// An encoder ffmpeg has never heard of, offered as hardware ahead of everything: it fails as soon as ffmpeg starts.
	cast.Encoders = func(ctx context.Context, codec media.Codec) (ffmpeg.Encoder, bool) {
		if codec != media.CodecH264 || slices.Contains(retired, "h264_bogus") {
			return software(ctx, codec)
		}
		asked = append(asked, "h264_bogus")
		return ffmpeg.Encoder{Name: "h264_bogus", Codec: media.CodecH264, Hardware: true, GOPKeyframes: true, AverageRate: true}, true
	}
	cast.Software = catalog.Software
	cast.RetireEncoder = func(name string) { retired = append(retired, name) }
	turns := &revisions{}

	ctx, cancel := context.WithTimeout(t.Context(), castTimeout)
	defer cancel()
	err = recovery.Cast(ctx, recovery.Intent{Candidates: []*source.Stream{stream}, Deadline: testReadDeadline, Turns: turns},
		cast, fixedOrigin{programFromStream(t, stream)})
	if err != nil {
		t.Fatalf("the cast failed instead of falling back to software: %v", err)
	}
	if !slices.Equal(turns.actions, []recovery.Action{recovery.SoftwareEncode}) {
		t.Fatalf("revisions %v, want exactly one software-encode", turns.actions)
	}
	if len(dev.served) == 0 {
		t.Fatal("the software retry served nothing")
	}
	if !slices.Equal(retired, []string{"h264_bogus"}) {
		t.Fatalf("retired %v: the encoder software proved at fault must be retired for the session", retired)
	}
	if len(asked) != 1 {
		t.Fatalf("the failed hardware encoder was planned %d times, want once", len(asked))
	}
	// Later casts no longer try it.
	if e, _ := cast.Encoders(ctx, media.CodecH264); e.Name == "h264_bogus" {
		t.Fatal("a retired encoder was offered to the next cast")
	}
}
