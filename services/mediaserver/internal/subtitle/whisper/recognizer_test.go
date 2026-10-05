package whisper

import (
	"io"
	"strings"
	"testing"

	wcpp "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

type languageContext struct {
	wcpp.Context
	language    string
	englishOnly bool
	processed   bool
}

func (c *languageContext) IsMultilingual() bool       { return !c.englishOnly }
func (c *languageContext) SetLanguage(s string) error { c.language = s; return nil }
func (*languageContext) SetVAD(bool)                  {}
func (*languageContext) SetVADModelPath(string)       {}
func (*languageContext) SetTokenTimestamps(bool)      {}
func (*languageContext) SetSplitOnWord(bool)          {}
func (*languageContext) SetMaxSegmentLength(uint)     {}
func (c *languageContext) Process([]float32, wcpp.EncoderBeginCallback, wcpp.SegmentCallback, wcpp.ProgressCallback) error {
	c.processed = true
	return nil
}
func (*languageContext) NextSegment() (wcpp.Segment, error) { return wcpp.Segment{}, io.EOF }

type languageModel struct {
	wcpp.Model
	context *languageContext
}

func (m languageModel) NewContext() (wcpp.Context, error) { return m.context, nil }

func TestAutomaticRecognitionEnablesLanguageDetection(t *testing.T) {
	// whisper_full_default_params starts with English pinned, even for a multilingual model.
	ctx := &languageContext{language: "en"}
	r := recognizer{model: languageModel{context: ctx}, language: "auto"}
	if _, err := r.Recognize(t.Context(), make([]float32, 1600), 0, ""); err != nil {
		t.Fatal(err)
	}
	if ctx.language != "auto" {
		t.Errorf("recognizer pinned %q, want automatic detection", ctx.language)
	}
}

func TestAutomaticRecognitionRejectsAnEnglishOnlyModelBeforeProcessing(t *testing.T) {
	ctx := &languageContext{language: "en", englishOnly: true}
	r := recognizer{model: languageModel{context: ctx}, language: "auto"}
	_, err := r.Recognize(t.Context(), make([]float32, 1600), 0, "")
	if err == nil || !strings.Contains(err.Error(), "supports English only") {
		t.Errorf("Recognize = %v, want the model's inability to detect language", err)
	}
	if ctx.processed {
		t.Error("recognition started on an English-only model despite automatic detection being requested")
	}
}
