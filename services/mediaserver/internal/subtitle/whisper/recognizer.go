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

// recognizer runs only inside serveWorker's serial loop. Native callback
// registries never receive concurrent calls within a worker process.
type recognizer struct {
	model        wcpp.Model
	language     string
	vadModelPath string
}

func (r recognizer) Recognize(ctx context.Context, samples []float32, offset float64) ([]subtitle.Word, error) {
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
	}

	// Silero VAD filters silence; whisper.cpp maps the timestamps back.
	wctx.SetVAD(true)
	wctx.SetVADModelPath(r.vadModelPath)

	// One token per segment: a segment's times are mapped back from the VAD's, a token's are not,
	// and words are rebuilt from the tokens so scripts written without spaces work too.
	wctx.SetTokenTimestamps(true)
	wctx.SetMaxSegmentLength(1)

	if err := wctx.Process(samples, func() bool { return ctx.Err() == nil }, nil, nil); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return nil, fmt.Errorf("whisper process: %w", err)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}

	var pieces []subtitle.Piece
	for {
		seg, err := wctx.NextSegment()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("whisper segment: %w", err)
		}
		var text strings.Builder
		for _, token := range seg.Tokens {
			if wctx.IsText(token) {
				text.WriteString(token.Text)
			}
		}
		pieces = append(pieces, subtitle.Piece{Start: seg.Start.Seconds() + offset, End: seg.End.Seconds() + offset, Text: text.String()})
	}
	return withoutAnnotations(subtitle.GroupPieces(pieces)), nil
}

// checkLanguage refuses a language an English-only model cannot transcribe, once, as the model loads.
func checkLanguage(model wcpp.Model, language string) error {
	if !model.IsMultilingual() && language != "en" {
		return fmt.Errorf("the configured whisper model supports English only, not %q", language)
	}
	return nil
}

// withoutAnnotations drops whisper's bracketed notes about what is not speech, such as [Music] or (laughs), and its music notes.
func withoutAnnotations(words []subtitle.Word) []subtitle.Word {
	var kept []subtitle.Word
	closer := "" // set inside a bracketed note, until the word that closes it
	for _, w := range words {
		if closer == "" {
			switch {
			case strings.HasPrefix(w.Text, "["):
				closer = "]"
			case strings.HasPrefix(w.Text, "("):
				closer = ")"
			case strings.HasPrefix(w.Text, "♪"):
				continue
			default:
				kept = append(kept, w)
				continue
			}
		}
		if strings.HasSuffix(w.Text, closer) {
			closer = ""
		}
	}
	return kept
}
