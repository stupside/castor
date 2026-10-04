package wire

import (
	"fmt"
	"net/http"
	"net/url"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// FromStream is the stream s names; the contract already holds its URL absolute.
func FromStream(s *castorv1.Stream) (*source.Stream, error) {
	u, err := url.Parse(s.GetUrl())
	if err != nil {
		return nil, fmt.Errorf("stream URL: %w", err)
	}
	var headers http.Header
	if len(s.GetHeaders()) > 0 {
		headers = make(http.Header, len(s.GetHeaders()))
		for k, v := range s.GetHeaders() {
			headers.Set(k, v)
		}
	}
	return &source.Stream{URL: u, Headers: headers, ContentType: s.GetContentType()}, nil
}

// FromCandidate translates playback data and discovery evidence independently.
func FromCandidate(c *castorv1.StreamCandidate) (*source.Stream, error) {
	stream, err := FromStream(c.GetStream())
	if err != nil {
		return nil, err
	}
	stream.Ladder = source.Ladder(c.GetLadder())
	return stream, nil
}

// FromDelivery is the delivery a cast asked for.
func FromDelivery(d castorv1.Delivery) compose.DeliveryPreference {
	if d == castorv1.Delivery_DELIVERY_SERVE {
		return compose.DeliveryServe
	}
	return compose.DeliveryAuto
}
