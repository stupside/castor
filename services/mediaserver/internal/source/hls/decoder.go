package hls

import (
	"context"
	"fmt"

	"github.com/stupside/castor/services/mediaserver/internal/media"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// Decoder interprets HLS resource encryption and nonstandard MPEG-TS segment envelopes.
// It holds no state; the relay supplies its bounded and cached key reader.
type Decoder struct{}

func (Decoder) ConsumesKey(key timeline.Key) bool { return decryptable(key) }

func (d Decoder) Segment(ctx context.Context, segment timeline.Segment, body []byte, keys timeline.KeyReader) ([]byte, media.HLSExtraSegmentFormat, error) {
	if d.ConsumesKey(segment.Key) {
		var err error
		body, err = decrypt(ctx, body, segment.Key, keys)
		if err != nil {
			return nil, 0, err
		}
	}
	return unwrapSegment(body)
}

func (d Decoder) Map(ctx context.Context, section timeline.Map, body []byte, keys timeline.KeyReader) ([]byte, error) {
	if !d.ConsumesKey(section.Key) {
		return body, nil
	}
	return decrypt(ctx, body, section.Key, keys)
}

func decrypt(ctx context.Context, body []byte, key timeline.Key, keys timeline.KeyReader) ([]byte, error) {
	secret, err := keys(ctx, key.URI)
	if err != nil {
		return nil, fmt.Errorf("reading the key: %w", err)
	}
	return decryptAES128(body, secret, key.IV)
}
