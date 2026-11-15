package execute

import (
	"reflect"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestAProgramWithNoEndIsHandedAsAStreamWithNoEnd(t *testing.T) {
	for _, tt := range []struct {
		name string
		p    presentation
		want Stream
	}{
		{"a source", presentation{source: true, contentType: media.HLS, live: true}, Source{ContentType: media.HLS, Live: true}},
		{"a playlist", presentation{contentType: media.HLS, segments: media.MP4, live: true}, Playlist{Segments: media.MP4, Live: true}},
		{"a served stream", presentation{contentType: media.MPEGTS, live: true}, Served{ContentType: media.MPEGTS, Live: true}},
		{"a source with an end", presentation{source: true, contentType: media.MP4, runtime: time.Hour}, Source{ContentType: media.MP4, Runtime: time.Hour}},
		{"a playlist with an end", presentation{contentType: media.HLS, segments: media.MP4, runtime: time.Hour}, Playlist{Runtime: time.Hour, Segments: media.MP4}},
		{"a served stream with an end", presentation{contentType: media.MPEGTS, runtime: time.Hour}, Served{ContentType: media.MPEGTS, Runtime: &Runtime{Length: time.Hour}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.stream(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("stream = %+v, want %+v", got, tt.want)
			}
		})
	}
}
