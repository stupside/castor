package device

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// describing is a family that answers what it accepts but refuses a session, so Describe must not open one.
type describing struct{ echoing }

func (describing) Connect(context.Context, Info) (Device, error) {
	return nil, errors.New("describing a device must not connect a session")
}

func (describing) Capabilities(context.Context, Info) (*mediav1.Capabilities, error) {
	return &mediav1.Capabilities{NativePlaybackTrial: true, Formats: []*mediav1.FormatSupport{{
		Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MKV}},
	}}}, nil
}

func TestDescribeReadsWhatTheDeviceAcceptsWithoutASession(t *testing.T) {
	target := &castorv1.Target{Target: &castorv1.Target_DeviceId{DeviceId: "push:uuid-1"}}
	d := directoryWithFamilies(Registry{Families: []Family{describing{}}, Timeout: time.Second})
	got, err := d.Describe(t.Context(), &castorv1.DescribeRequest{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetFidelity() != castorv1.Fidelity_FIDELITY_ORIGINAL_FILE || !slices.Equal(got.GetOriginalContentTypes(), []string{"video/x-matroska"}) {
		t.Fatalf("Describe = %v", got)
	}
	silent := directoryWithFamilies(Registry{Families: []Family{echoing{}}, Timeout: time.Second})
	if _, err := silent.Describe(t.Context(), &castorv1.DescribeRequest{Target: target}); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("a family that cannot describe without a session answered %v, want unimplemented", err)
	}
}

func TestFidelityIsWhatANativeCastKeepsOfAnySource(t *testing.T) {
	progressive := func(c mediav1.ProgressiveSupport_Container) *mediav1.FormatSupport {
		return &mediav1.FormatSupport{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: c}}}
	}
	hls := &mediav1.FormatSupport{Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}}}
	for _, tt := range []struct {
		name      string
		caps      *mediav1.Capabilities
		want      castorv1.Fidelity
		originals []string
	}{
		{"a renderer naming containers but not codecs is forwarded the file",
			&mediav1.Capabilities{NativePlaybackTrial: true, Formats: []*mediav1.FormatSupport{
				progressive(mediav1.ProgressiveSupport_CONTAINER_MPEGTS), progressive(mediav1.ProgressiveSupport_CONTAINER_MP4),
				progressive(mediav1.ProgressiveSupport_CONTAINER_MP4), progressive(mediav1.ProgressiveSupport_CONTAINER_MKV),
			}},
			castorv1.Fidelity_FIDELITY_ORIGINAL_FILE, []string{"video/mp2t", "video/mp4", "video/x-matroska"}},
		{"a self-fetching trial receiver keeps the tracks in castor's container",
			&mediav1.Capabilities{SelfFetch: true, NativePlaybackTrial: true, Formats: []*mediav1.FormatSupport{progressive(mediav1.ProgressiveSupport_CONTAINER_MP4), hls}},
			castorv1.Fidelity_FIDELITY_ORIGINAL_TRACKS, nil},
		{"a receiver judged by its stated codecs alone converts what it never named",
			&mediav1.Capabilities{Formats: []*mediav1.FormatSupport{progressive(mediav1.ProgressiveSupport_CONTAINER_MPEGTS)}},
			castorv1.Fidelity_FIDELITY_CONVERTED, nil},
		{"a playlist-only trial device has no file container", &mediav1.Capabilities{NativePlaybackTrial: true, Formats: []*mediav1.FormatSupport{hls}},
			castorv1.Fidelity_FIDELITY_ORIGINAL_TRACKS, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := Fidelity(tt.caps)
			if got.GetFidelity() != tt.want || !slices.Equal(got.GetOriginalContentTypes(), tt.originals) {
				t.Fatalf("Fidelity = %v %v, want %v %v", got.GetFidelity(), got.GetOriginalContentTypes(), tt.want, tt.originals)
			}
		})
	}
}
