// Package whisper transcribes a cast's sound with whisper.cpp.
package whisper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/stupside/castor/internal/telemetry"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
	"golang.org/x/sync/semaphore"
)

// Config holds settings for the isolated whisper.cpp transcriber.
type Config struct {
	ModelPath string `yaml:"model_path"` // override the auto-downloaded base model (base.en for English)
}

// Transcript owns speech recognition and its committed captions. Burn-in is an optional consumer.
type Transcript struct {
	config workerConfig

	cues *subtitle.Cues
}

// New transcribes in language, a whisper code or auto to detect it.
func New(ctx context.Context, cfg Config, language string) (*Transcript, error) {
	modelPath, err := ensureModel(ctx, cfg.ModelPath, language)
	if err != nil {
		return nil, err
	}
	vadModelPath, err := ensure(ctx, vadModelName, vadModelBaseURL)
	if err != nil {
		return nil, err
	}
	cues := &subtitle.Cues{}
	return &Transcript{
		config: workerConfig{Language: language, ModelPath: modelPath, VADPath: vadModelPath},
		cues:   cues,
	}, nil
}

// Run transcribes pcm until it ends; on failure it drains pcm so the reader never blocks on it.
func (b *Transcript) Run(ctx context.Context, pcm io.ReadCloser) {
	ctx, finish := telemetry.Start(ctx, telemetry.Transcribe)
	closePCM := sync.OnceFunc(func() { _ = pcm.Close() })
	stop := context.AfterFunc(ctx, closePCM)
	defer stop()
	defer closePCM()
	err := b.transcribe(ctx, pcm)
	finish(err)
	if err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "transcription failed; subtitles stop here", "error", err)
		_, _ = io.Copy(io.Discard, pcm)
	}
}

var modelAdmissions = semaphore.NewWeighted(2)

func (b *Transcript) transcribe(ctx context.Context, pcm io.Reader) error {
	if !modelAdmissions.TryAcquire(1) {
		b.cues.Close()
		return fmt.Errorf("loading whisper model: concurrent model capacity exhausted")
	}
	defer modelAdmissions.Release(1)
	slog.InfoContext(ctx, "starting isolated transcription", "model", b.config.ModelPath)
	worker := newWorker(b.config)
	defer worker.Close()
	return subtitle.Transcribe(ctx, pcm, worker, b.cues)
}

// Language is the code transcribed in.
func (b *Transcript) Language() string { return b.config.Language }

// Cues supplies timed captions independently of the selected renderer.
func (b *Transcript) Cues() *subtitle.Cues { return b.cues }

// LatestEnd is the settled caption frontier, including confirmed silence.
func (b *Transcript) LatestEnd() float64 { return b.cues.Snapshot(0, 0).SettledTo }

// Done reports whether the transcription ended, so no further words will appear.
func (b *Transcript) Done() bool { return b.cues.Snapshot(0, 0).Complete }

// SampleRate is the rate of the PCM feed Run reads.
func (b *Transcript) SampleRate() int { return subtitle.SampleRate }
