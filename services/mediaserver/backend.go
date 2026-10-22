package mediaserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/cast"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	hlsoutput "github.com/stupside/castor/services/mediaserver/internal/cast/output/hls"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/progressive"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/probe"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/dash"
	"github.com/stupside/castor/services/mediaserver/internal/source/follow"
	"github.com/stupside/castor/services/mediaserver/internal/source/hls"
	"github.com/stupside/castor/services/mediaserver/internal/source/rank"
	"github.com/stupside/castor/services/mediaserver/internal/source/web"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle/whisper"
	"github.com/stupside/castor/services/mediaserver/internal/wire"
)

// formats is every source format castor reads, in the order a source is offered to them.
var formats = source.Formats{hls.Format{}, dash.Format{}}

const (
	// fetchTimeout bounds each playlist fetch and each probe.
	fetchTimeout = 30 * time.Second
	// readDeadline is the mid-read stall bound every fetch plan is derived with.
	readDeadline = 30 * time.Second
)

// backend is the machinery the media server casts with, bound to c.
func (c *Config) backend() func(*mediav1.PlaybackSettings) cast.Caster {
	ffmpegBinary, ffprobeBinary := c.Servers.Media.FFmpeg.Path, c.Servers.Media.FFmpeg.ProbePath
	machinery := execute.Machinery{
		Outputs:    output.Factory{progressive.Adapter{}, hlsoutput.Adapter{}},
		FFmpegPath: ffmpegBinary,
		Binary:     ffmpeg.Inspect(ffmpegBinary),
		Encoders:   ffmpeg.Encoders(ffmpegBinary),
		Probes:     probe.FFprobe(ffprobeBinary),
		InputArgs:  formats.InputArgs,
	}
	resolverConfig, whisperConfig := c.Servers.Media.Resolver.Config, c.Servers.Media.Whisper
	return func(asked *mediav1.PlaybackSettings) cast.Caster {
		client := web.New(fetchTimeout)
		timelines := follow.New(client, formats, follow.Repackager(ffmpegBinary))
		resolver := resolverWithProbe(client, media.HeightCap(asked.GetMaxHeight()), asked.GetAudio().GetLanguages(), machinery.Probes)
		return caster{
			Ranker: rank.New(resolverConfig, media.HeightCap(asked.GetMaxHeight()), func(ctx context.Context, s *source.Stream) (source.Inspection, error) {
				return (candidateProbe{stream: s, client: client, resolver: resolver, timelines: timelines, binary: machinery.Probes}).Inspect(ctx)
			}),
			machinery:     machinery,
			client:        client,
			timelines:     timelines,
			transcription: transcription(whisperConfig, asked.GetSubtitles()),
			asked:         asked,
		}
	}
}

// transcription selects a lazy recognizer factory for each resolved audio language.
func transcription(cfg whisper.Config, selection *castorv1.SubtitleSelection) func(context.Context, string) execute.Subtitles {
	if !selection.GetPreferences().GetTranscribeIfMissing() {
		return nil
	}
	preferred := selection.GetPreferences().GetLanguages()
	return func(ctx context.Context, audioLanguage string) execute.Subtitles {
		language := transcriptLanguage(preferred, audioLanguage)
		if language == "" {
			slog.WarnContext(ctx, "transcription skipped: selected audio language is not among subtitle preferences", "audio_language", audioLanguage, "preferred_languages", preferred)
			return nil
		}
		return func(ctx context.Context) execute.Transcript {
			b, err := whisper.New(ctx, cfg, language)
			if err != nil {
				slog.WarnContext(ctx, "whisper init failed; casting without subtitles", "error", err)
				return nil
			}
			return b
		}
	}
}

// transcriptLanguage follows the selected audio when its language is declared.
// Without that fact, the first subtitle preference is the explicit best guess.
func transcriptLanguage(preferred []string, audioLanguage string) string {
	if audioLanguage != "" {
		for _, language := range preferred {
			if source.SameLanguage(audioLanguage, language) {
				return language
			}
		}
		return ""
	}
	if len(preferred) > 0 {
		return preferred[0]
	}
	return ""
}

// captions shows the source's own captions in the selected language, none when the selection disables them.
func (k caster) captions(selection *castorv1.SubtitleSelection) *execute.Captions {
	if selection.GetDisabled() != nil {
		return nil
	}
	return &execute.Captions{Languages: selection.GetPreferences().GetLanguages(), Client: k.client}
}

// caster ranks and plays one cast as it was asked.
type caster struct {
	*rank.Ranker
	machinery     execute.Machinery
	client        source.Client
	timelines     execute.Timelines
	transcription func(context.Context, string) execute.Subtitles
	asked         *mediav1.PlaybackSettings
}

// Rank ranks streams by the content types their MIME types announce.
func (k caster) Rank(ctx context.Context, streams []*source.Stream) ([]*source.Stream, error) {
	announced := make([]*source.Stream, len(streams))
	for i, s := range streams {
		announced[i] = announce(s)
	}
	return k.Ranker.Rank(ctx, announced)
}

// Measure measures stream by the content type its MIME type announces.
func (k caster) Measure(ctx context.Context, stream *source.Stream) (*source.Stream, error) {
	return k.Ranker.Measure(ctx, announce(stream))
}

// announce reads a MIME type castor does not recognize as no evidence: the link's name or body decides.
func announce(s *source.Stream) *source.Stream {
	announced := *s
	announced.ContentType = formats.Announced(s.ContentType)
	return &announced
}

func (k caster) Play(ctx context.Context, device execute.Device, publisher output.Publisher, streams []*source.Stream, turns recovery.Turns) error {
	run := execute.Cast{
		Machinery:     k.machinery,
		Timelines:     k.timelines,
		Transcription: k.transcription,
		Device:        device,
		Publisher:     publisher,
		Captions:      k.captions(k.asked.GetSubtitles()),
		MaxHeight:     media.HeightCap(k.asked.GetMaxHeight()),
	}
	// The ranked streams, head first; the rest are what recovery switches to.
	return recovery.Cast(ctx, recovery.Intent{
		Candidates: streams,
		Deadline:   readDeadline,
		Delivery:   wire.FromDelivery(k.asked.GetDelivery()),
		Turns:      turns,
	}, run, resolverWithProbe(k.client, run.MaxHeight, k.asked.GetAudio().GetLanguages(), k.machinery.Probes))
}

func resolverWithProbe(client source.Client, height media.HeightCap, languages []string, binary probe.FFprobe) *source.Resolver {
	measure := func(ctx context.Context, u *url.URL, h http.Header) (media.ProbeInfo, error) {
		info, _, err := binary.Link(probe.Link{URL: u, Headers: h, InputArgs: formats.InputArgs(media.HLS, 0)}, fetchTimeout).Probe(ctx)
		return info, err
	}
	return source.NewResolver(client, height, languages, source.Formats{hls.Format{ProbeRendition: measure}, dash.Format{}})
}
