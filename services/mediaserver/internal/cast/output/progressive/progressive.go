// Package progressive adapts growing byte streams to the output port.
package progressive

import (
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

type Adapter struct{}

func (Adapter) Select(f media.FormatSupport, c *output.Captions) (output.Plan, bool) {
	if c != nil {
		return nil, false
	}
	switch f.Transport {
	case media.TransportMP4, media.TransportMPEGTS:
		format, _ := container.For(f.Transport.ContentType())
		return streamPlan{format}, true
	}
	return nil, false
}

type streamPlan struct{ format container.Format }

func (p streamPlan) Format() container.Format        { return p.format }
func (p streamPlan) EncoderFormat() container.Format { return p.format }
func (p streamPlan) Pace(burn bool) fetch.Pace {
	if burn {
		return fetch.Pace{Realtime: 1.15, Burst: fetch.EncodeBurst}
	}
	return fetch.Pace{}
}
func (p streamPlan) Splices() bool {
	return p.format.Framing == media.FramingInBand
}
func (streamPlan) Reserve(string) (func() error, error) { return nil, nil }
func (p streamPlan) Prepare(o output.Opening, dir string) (output.Prepared, error) {
	o.Format = p.format
	srv, err := NewStream(o, dir)
	if err != nil {
		return output.Prepared{}, err
	}
	return output.Prepared{Output: srv, Write: srv.Write}, nil
}
func (p streamPlan) ServeSpool(o output.Opening, sp *storage.Spool) output.Output {
	o.Format = p.format
	return ServeSpool(o, sp)
}
