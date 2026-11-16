package whisper

import (
	"io"
	"strings"
	"testing"

	wcpp "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"

	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

type languageContext struct {
	wcpp.Context
	language string
}

func (*languageContext) IsMultilingual() bool         { return true }
func (c *languageContext) SetLanguage(s string) error { c.language = s; return nil }
func (*languageContext) SetVAD(bool)                  {}
func (*languageContext) SetVADModelPath(string)       {}
func (*languageContext) SetTokenTimestamps(bool)      {}
func (*languageContext) SetSplitOnWord(bool)          {}
func (*languageContext) SetMaxSegmentLength(uint)     {}
func (*languageContext) Process([]float32, wcpp.EncoderBeginCallback, wcpp.SegmentCallback, wcpp.ProgressCallback) error {
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
	if _, err := r.Recognize(t.Context(), make([]float32, 1600), 0); err != nil {
		t.Fatal(err)
	}
	if ctx.language != "auto" {
		t.Errorf("recognizer pinned %q, want automatic detection", ctx.language)
	}
}

type modelOf struct {
	wcpp.Model
	multilingual bool
}

func (m modelOf) IsMultilingual() bool { return m.multilingual }

func TestAnEnglishOnlyModelIsRefusedAtLoadForAnyOtherLanguage(t *testing.T) {
	for _, tc := range []struct {
		multilingual bool
		language     string
		refused      bool
	}{
		{false, "auto", true}, {false, "fr", true}, {false, "en", false}, {true, "auto", false},
	} {
		err := checkLanguage(modelOf{multilingual: tc.multilingual}, tc.language)
		if (err != nil) != tc.refused || (err != nil && !strings.Contains(err.Error(), "supports English only")) {
			t.Errorf("multilingual %v, language %q: checkLanguage = %v, want refused %v", tc.multilingual, tc.language, err, tc.refused)
		}
	}
}

func TestAnnotationsOfWhatIsNotSpeechAreDropped(t *testing.T) {
	var words []subtitle.Word
	for w := range strings.FieldsSeq("[Music] Hello (he laughs) there ♪ [BLANK_AUDIO] (speaking in foreign language) friend") {
		words = append(words, subtitle.Word{Text: w})
	}
	var got []string
	for _, w := range withoutAnnotations(words) {
		got = append(got, w.Text)
	}
	if want := "Hello there friend"; strings.Join(got, " ") != want {
		t.Errorf("withoutAnnotations = %q, want %q", got, want)
	}
}
