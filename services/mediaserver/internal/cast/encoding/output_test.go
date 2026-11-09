package encoding

import (
	"context"
	"errors"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

type outputProbe struct {
	info media.ProbeInfo
	err  error
}

func (p outputProbe) Probe(context.Context) (media.ProbeInfo, media.Reach, error) {
	return p.info, media.Reach(0), p.err
}

func TestEncodedOutputMustMatchTheReceiverEnvelope(t *testing.T) {
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264, Profiles: []media.Profile{media.ProfileBaseline}, MaxLevel: 31}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}
	p := media.ProbeInfo{VideoCodec: media.CodecH264, VideoProfile: media.ProfileHigh, VideoBitDepth: 8, VideoLevel: 31, AudioCodec: media.CodecAAC, AudioChannels: 2}
	if err := ValidateOutput(t.Context(), outputProbe{info: p}, caps, media.MP4, p.RequiredTracks(), nil); err == nil {
		t.Fatal("encoder produced incompatible High profile without refusal")
	}
	p.VideoProfile = media.ProfileBaseline
	if err := ValidateOutput(t.Context(), outputProbe{info: p}, caps, media.MP4, p.RequiredTracks(), nil); err != nil {
		t.Fatal(err)
	}
	p.VideoLevel = 42
	if err := ValidateOutput(t.Context(), outputProbe{info: p}, caps, media.MP4, p.RequiredTracks(), nil); err == nil {
		t.Fatal("encoder exceeded receiver level")
	}
}

func TestEncodedOutputValidationEndsOnCancellationOrCompletedBadOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	probeErr := errors.New("no readable streams")
	if err := ValidateOutput(ctx, outputProbe{err: probeErr}, media.Capabilities{}, media.MP4, media.TrackRequirements{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	done := make(chan struct{})
	close(done)
	if err := ValidateOutput(t.Context(), outputProbe{err: probeErr}, media.Capabilities{}, media.MP4, media.TrackRequirements{}, done); !errors.Is(err, probeErr) {
		t.Fatalf("got %v", err)
	}
}

type finishingOutputProbe struct {
	done  chan struct{}
	calls int
	info  media.ProbeInfo
}

func (p *finishingOutputProbe) Probe(context.Context) (media.ProbeInfo, media.Reach, error) {
	p.calls++
	if p.calls == 1 {
		close(p.done)
		return media.ProbeInfo{}, media.ReachOpened, errors.New("footer not written yet")
	}
	return p.info, media.ReachOpened, nil
}
func TestOutputValidationProbesCompletedArtifactAfterStartupRace(t *testing.T) {
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	p := &finishingOutputProbe{done: make(chan struct{}), info: media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoProfile: media.ProfileHigh}}
	if err := ValidateOutput(t.Context(), p, caps, media.MP4, p.info.RequiredTracks(), p.done); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("probed %d times, want initial and final", p.calls)
	}
}
func TestCanceledValidationCannotAcceptSuccessfulProbe(t *testing.T) {
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	info := media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoProfile: media.ProfileHigh}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ValidateOutput(ctx, outputProbe{info: info}, caps, media.MP4, info.RequiredTracks(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("validation=%v", err)
	}
}
func TestCompletedOutputMismatchStillFailsAfterStartupRace(t *testing.T) {
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264, Profiles: []media.Profile{media.ProfileBaseline}}}}}
	p := &finishingOutputProbe{done: make(chan struct{}), info: media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoProfile: media.ProfileHigh}}
	if err := ValidateOutput(t.Context(), p, caps, media.MP4, p.info.RequiredTracks(), p.done); err == nil {
		t.Fatal("final incompatible profile accepted")
	}
}

// ffprobe succeeds on an fMP4 initialization header before frames establish the
// profile, level and pixel format. Completion must trigger one final measurement.
type headerOutputProbe struct {
	done  chan struct{}
	calls int
}

func (p *headerOutputProbe) Probe(context.Context) (media.ProbeInfo, media.Reach, error) {
	p.calls++
	info := media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 360, VideoLevel: -99}
	if p.calls == 1 {
		close(p.done)
	} else {
		info.VideoProfile, info.VideoBitDepth, info.VideoLevel = media.ProfileHigh, 8, 31
	}
	return info, media.ReachOpened, nil
}
func TestOutputValidationWaitsForCodecParameters(t *testing.T) {
	p := &headerOutputProbe{done: make(chan struct{})}
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264, Profiles: []media.Profile{media.ProfileHigh}, MaxLevel: 31}}}}
	if err := ValidateOutput(t.Context(), p, caps, media.MP4, media.TrackRequirements{Video: true}, p.done); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("probes = %d, want header and completed output", p.calls)
	}
}

func TestOutputValidationDoesNotRequireH264ParametersForVP8(t *testing.T) {
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportWebM, Video: media.VideoSupport{Codec: media.CodecVP8}}}}
	info := media.ProbeInfo{VideoCodec: media.CodecVP8, VideoBitDepth: 8, VideoLevel: -99}
	if err := ValidateOutput(t.Context(), outputProbe{info: info}, caps, media.WebM, info.RequiredTracks(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestCompletedHeaderWithoutVideoParametersIsRejected(t *testing.T) {
	done := make(chan struct{})
	close(done)
	info := media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 360, VideoLevel: -99}
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	if err := ValidateOutput(t.Context(), outputProbe{info: info}, caps, media.MP4, info.RequiredTracks(), done); err == nil {
		t.Fatal("header-only output was accepted")
	}
}

func TestBufferedReadMustRetainTheSourcesVideo(t *testing.T) {
	done := make(chan struct{})
	close(done)
	expected := media.TrackRequirements{Video: true, Audio: true}
	spool := media.ProbeInfo{AudioCodec: media.CodecAAC}
	if err := ValidateTracks(t.Context(), outputProbe{info: spool}, expected, done); err == nil {
		t.Fatal("audio-only spool accepted after source video disappeared")
	}
	if err := ValidateTracks(t.Context(), outputProbe{info: spool}, media.TrackRequirements{Audio: true}, done); err != nil {
		t.Fatalf("valid audio-only spool rejected: %v", err)
	}
}

func TestBufferedReadRejectsUnidentifiedExtraTracks(t *testing.T) {
	done := make(chan struct{})
	close(done)
	for _, info := range []media.ProbeInfo{
		{VideoPresent: true, AudioCodec: media.CodecAAC},
		{AudioPresent: true, VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoProfile: media.ProfileHigh},
	} {
		if err := ValidateTracks(t.Context(), outputProbe{info: info}, media.TrackRequirements{}, done); err == nil {
			t.Fatalf("unidentified stream passed output readiness: %+v", info)
		}
	}
}

func TestAnOutputTheReceiverCannotPlayNamesTheHalvesItRefuses(t *testing.T) {
	caps := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}, Audio: media.AudioSupport{Codec: media.CodecAAC, MaxChannels: 2}}}}
	p := media.ProbeInfo{VideoCodec: media.CodecVP8, VideoBitDepth: 8, AudioCodec: media.CodecAAC, AudioChannels: 2}
	mismatch, ok := errors.AsType[*Mismatch](ValidateOutput(t.Context(), outputProbe{info: p}, caps, media.MP4, p.RequiredTracks(), nil))
	if !ok || mismatch.Refused != (media.Axes{Video: true}) {
		t.Fatalf("mismatch = %+v, want the picture named as what the receiver refuses", mismatch)
	}
}
