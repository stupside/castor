package whisper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	wcpp "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"

	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// recognizer runs one loaded whisper model over each window it is handed.
type recognizer struct {
	model        wcpp.Model
	language     string
	vadModelPath string
}

func (r recognizer) Recognize(ctx context.Context, samples []float32, offset float64, prompt string) ([]subtitle.Word, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	wctx, err := r.model.NewContext()
	if err != nil {
		return nil, fmt.Errorf("new whisper context: %w", err)
	}

	// New contexts default to English; automatic detection must be requested explicitly too.
	if wctx.IsMultilingual() {
		if err := wctx.SetLanguage(r.language); err != nil {
			return nil, fmt.Errorf("setting whisper language %q: %w", r.language, err)
		}
	} else if r.language != "en" {
		return nil, fmt.Errorf("the configured whisper model supports English only, not %q", r.language)
	}

	// Silero VAD filters silence; whisper.cpp maps the timestamps back.
	wctx.SetVAD(true)
	wctx.SetVADModelPath(r.vadModelPath)

	// One word per segment, since LocalAgreement matches word by word.
	wctx.SetTokenTimestamps(true)
	wctx.SetSplitOnWord(true)
	wctx.SetMaxSegmentLength(1)

	if prompt != "" {
		wctx.SetInitialPrompt(prompt)
	}

	if err := wctx.Process(samples, func() bool { return ctx.Err() == nil }, nil, nil); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return nil, fmt.Errorf("whisper process: %w", err)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}

	var words []subtitle.Word
	for {
		seg, err := wctx.NextSegment()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("whisper segment: %w", err)
		}
		if isNoise(seg.Text) {
			continue
		}
		words = append(words, subtitle.Word{
			Start: seg.Start.Seconds() + offset,
			End:   seg.End.Seconds() + offset,
			Text:  seg.Text,
		})
	}
	return words, nil
}

// isNoise is whisper's annotation of what is not speech, or the empty text of borderline audio.
func isNoise(s string) bool {
	if s == "" {
		return true
	}
	return s[0] == '[' || s[0] == '(' || strings.HasPrefix(s, "♪")
}
