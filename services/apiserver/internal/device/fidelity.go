package device

import (
	"context"
	"fmt"
	"slices"

	"connectrpc.com/connect"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// Describer reads what a device accepts without opening a session that could change what it shows.
type Describer interface {
	Capabilities(ctx context.Context, info Info) (*mediav1.Capabilities, error)
}

// Describe reads what the device at target accepts, as its family locates it.
func (r Registry) Describe(ctx context.Context, target Info) (*mediav1.Capabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	f, err := r.family(target.Type)
	if err != nil {
		return nil, err
	}
	describer, ok := f.(Describer)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("%s devices cannot be described without a cast", target.Type))
	}
	info, err := locate(ctx, f, target)
	if err != nil {
		return nil, err
	}
	return describer.Capabilities(ctx, info)
}

// Describe reports the most of a source castor can hand the device t names, without casting.
func (d *Directory) Describe(ctx context.Context, req *castorv1.DescribeRequest) (*castorv1.DescribeResponse, error) {
	info, err := d.Target(ctx, req.GetTarget())
	if err != nil {
		return nil, err
	}
	caps, err := d.reach.Describe(ctx, info)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnimplemented {
			return nil, err
		}
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("asking the device what it accepts: %w", err))
	}
	return Fidelity(caps), nil
}

// Fidelity is what a Native cast keeps of a source whose tracks caps state nothing about. It mirrors the
// media server's composition: a device that leaves tracks to its own verdict is forwarded the source file,
// or handed it where it fetches for itself, in any container it names.
func Fidelity(caps *mediav1.Capabilities) *castorv1.DescribeResponse {
	if !caps.GetNativePlaybackTrial() {
		return &castorv1.DescribeResponse{Fidelity: castorv1.Fidelity_FIDELITY_CONVERTED}
	}
	var originals []string
	for _, f := range caps.GetFormats() {
		if mime := progressiveMIME(f.GetProgressive().GetContainer()); mime != "" && !slices.Contains(originals, mime) {
			originals = append(originals, mime)
		}
	}
	if len(originals) == 0 {
		return &castorv1.DescribeResponse{Fidelity: castorv1.Fidelity_FIDELITY_ORIGINAL_TRACKS}
	}
	return &castorv1.DescribeResponse{Fidelity: castorv1.Fidelity_FIDELITY_ORIGINAL_FILE, OriginalContentTypes: originals}
}

// progressiveMIME names a progressive container the way the play contract does, empty for none.
func progressiveMIME(c mediav1.ProgressiveSupport_Container) string {
	switch c {
	case mediav1.ProgressiveSupport_CONTAINER_MP4:
		return MIME(mediav1.Container_CONTAINER_MP4)
	case mediav1.ProgressiveSupport_CONTAINER_MPEGTS:
		return MIME(mediav1.Container_CONTAINER_MPEGTS)
	case mediav1.ProgressiveSupport_CONTAINER_MKV:
		return MIME(mediav1.Container_CONTAINER_MKV)
	case mediav1.ProgressiveSupport_CONTAINER_WEBM:
		return MIME(mediav1.Container_CONTAINER_WEBM)
	}
	return ""
}
