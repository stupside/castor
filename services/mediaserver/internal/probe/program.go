package probe

import (
	"context"
	"fmt"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Input is one input of a program as ffprobe opens it, in the program's input order.
type Input struct {
	ID   media.InputID
	URL  string
	Args []string
}

// Source binds a program's inputs to this ffprobe, each opened exactly as its reader will.
func (bin FFprobe) Source(program media.Program, inputs []Input) media.Prober {
	return sourceProber{ffprobePath: string(bin), program: program, inputs: inputs}
}

type sourceProber struct {
	ffprobePath string
	program     media.Program
	inputs      []Input
}

// Probe measures a demuxed program (audio in companion rendition; silence if audio can't be probed).
func (p sourceProber) Probe(ctx context.Context) (media.ProbeInfo, media.Reach, error) {
	program, inputs := p.program, p.inputs
	var info media.ProbeInfo
	video, videoInput, hasVideo := program.TrackInput(media.TrackVideo)
	audio, audioInput, hasAudio := program.TrackInput(media.TrackAudio)

	// Open each selected input once; choose kind-relative tracks from complete JSON (signed URL single use).
	for inputIndex, input := range inputs {
		videoIndex, audioIndex := -1, -1
		if hasVideo && videoInput == inputIndex {
			videoIndex = video.Index
		}
		if hasAudio && audioInput == inputIndex {
			audioIndex = audio.Index
		}
		clock := input.ID == program.ClockInput
		if videoIndex < 0 && audioIndex < 0 && !clock {
			continue
		}

		measured, reach, err := pass{
			ffprobePath: p.ffprobePath,
			inputArgs:   input.Args,
			input:       input.URL,
			videoIndex:  videoIndex,
			audioIndex:  audioIndex,
		}.run(ctx)
		if err != nil {
			switch {
			case videoIndex >= 0:
				return media.ProbeInfo{}, reach, fmt.Errorf("probing selected video input: %w", err)
			case audioIndex >= 0:
				return info, reach, fmt.Errorf("probing selected audio input: %w", err)
			default:
				return info, reach, fmt.Errorf("probing program clock input: %w", err)
			}
		}
		// A start ffprobe could not read is no zero: reading it as one would shift this input by the clock's start.
		if measured.StartKnown {
			if info.InputStarts == nil {
				info.InputStarts = map[media.InputID]time.Duration{}
			}
			info.InputStarts[input.ID] = measured.Start
		}
		if clock {
			info.Start = measured.Start
			info.StartKnown = measured.StartKnown
			info.ContentType = measured.ContentType
			info.BitRate = measured.BitRate
			info.Duration = measured.Duration
		}
		// The ladder travels with the video half (worth most where alternatives are measured).
		if videoIndex >= 0 {
			info = info.TakeVideo(measured)
		}
		if audioIndex >= 0 {
			info = info.TakeAudio(measured)
		}
	}

	if hasVideo && !info.HasVideo() && !video.Optional {
		return info, media.ReachOpened, fmt.Errorf("selected video track V:%d is absent from input %d", video.Index, videoInput)
	}
	if hasAudio && !info.HasAudio() && !audio.Optional {
		return info, media.ReachOpened, fmt.Errorf("selected audio track a:%d is absent from input %d", audio.Index, audioInput)
	}
	return info, media.ReachOpened, nil
}
