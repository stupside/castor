// Package whisper transcribes a cast's sound with whisper.cpp.
package whisper

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	wcpp "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// Config holds settings for the in-process whisper.cpp transcriber.
type Config struct {
	ModelPath string `yaml:"model_path"` // override the auto-downloaded tiny model (tiny.en for English)
}

// Burn is the running transcription: model, committed cues, and the file the encoder reads.
type Burn struct {
	language     string
	modelPath    string
	vadModelPath string

	transcription subtitle.Transcription
	cues          *subtitle.Cues
	file          *subtitle.CueFile
}

// New transcribes in language, a whisper code or auto to detect it, buffering its cues in workDir.
func New(ctx context.Context, cfg Config, language, workDir string) (*Burn, error) {
	modelPath, err := ensureModel(ctx, cfg.ModelPath, language)
	if err != nil {
		return nil, err
	}
	vadModelPath, err := ensure(ctx, vadModelName, vadModelBaseURL)
	if err != nil {
		return nil, err
	}
	cues := &subtitle.Cues{}
	return &Burn{
		language:     language,
		modelPath:    modelPath,
		vadModelPath: vadModelPath,
		cues:         cues,
		file:         subtitle.NewCueFile(workDir, cues),
	}, nil
}

// Run transcribes pcm until it ends; on failure it drains pcm so the reader never blocks on it.
func (b *Burn) Run(ctx context.Context, pcm io.ReadCloser) {
	stop := context.AfterFunc(ctx, func() { _ = pcm.Close() })
	defer stop()
	defer pcm.Close()
	if err := b.transcription.Run(ctx, pcm, b.load, b.cues); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "transcription failed; subtitles stop here", "error", err)
		_, _ = io.Copy(io.Discard, pcm)
	}
}

func (b *Burn) load(ctx context.Context) (subtitle.Recognizer, func(), error) {
	slog.InfoContext(ctx, "loading whisper model", "path", b.modelPath, "vad", b.vadModelPath)
	model, err := wcpp.New(b.modelPath)
	if err != nil {
		return nil, nil, fmt.Errorf("loading whisper model: %w", err)
	}
	return recognizer{model: model, language: b.language, vadModelPath: b.vadModelPath}, func() { _ = model.Close() }, nil
}

// LatestEnd is the end of the last committed word, in seconds.
func (b *Burn) LatestEnd() float64 { return b.transcription.LatestEnd() }

// Done reports whether the transcription ended, so no further words will appear.
func (b *Burn) Done() bool { return b.transcription.Done() }

// SampleRate is the rate of the PCM feed Run reads.
func (b *Burn) SampleRate() int { return subtitle.SampleRate }

// Inputs creates the live cue file and returns its path; the video decision's burn-in input.
func (b *Burn) Inputs() (string, error) { return b.file.Create() }

func (b *Burn) Follow(ctx context.Context) func(media.Progress) { return b.file.Writer(ctx) }
