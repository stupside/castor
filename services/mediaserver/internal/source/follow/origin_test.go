package follow

import (
	"context"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

type markedDecoder struct{}

func (markedDecoder) ConsumesKey(key timeline.Key) bool { return key.Method == "marked" }

func (markedDecoder) Segment(ctx context.Context, _ timeline.Segment, body []byte, keys timeline.KeyReader) ([]byte, media.HLSExtraSegmentFormat, error) {
	key, err := keys(ctx, "key")
	if err != nil {
		return nil, 0, err
	}
	return append(body, key...), media.HLSExtraSegmentTIKTIKPX, nil
}

func (markedDecoder) Map(ctx context.Context, _ timeline.Map, body []byte, keys timeline.KeyReader) ([]byte, error) {
	key, err := keys(ctx, "key")
	if err != nil {
		return nil, err
	}
	return append(body, key...), nil
}

func TestRelayUsesFormatDecoderAndSharesItsKeyRead(t *testing.T) {
	origin := &stored{resources: map[string][]byte{"segment": []byte("media"), "init": []byte("header"), "key": []byte("secret")}}
	f := newFeed(t.Context(), "primary", timeline.Reading{Source: origin, Decoder: markedDecoder{}}, nil)
	segment := timeline.Segment{URI: "segment", Key: timeline.Key{Method: "marked", URI: "key"}}
	got, err := f.read(t.Context(), segment)
	if err != nil || string(got) != "mediasecret" || f.unwrapped.Load() != uint32(media.HLSExtraSegmentTIKTIKPX) {
		t.Fatalf("segment = %q, observed = %d, error = %v", got, f.unwrapped.Load(), err)
	}
	init, err := f.initBytes(t.Context(), timeline.Map{URI: "init", Key: segment.Key})
	if err != nil || string(init) != "headersecret" {
		t.Fatalf("init = %q, error = %v", init, err)
	}
	if origin.reads["key"] != 1 {
		t.Errorf("key fetched %d times, want one shared read", origin.reads["key"])
	}
	f.repackaging = new(false)
	if relayed := f.view(segment, 0).Key; relayed != (timeline.Key{}) {
		t.Errorf("consumed key still advertised to the reader: %+v", relayed)
	}
}
