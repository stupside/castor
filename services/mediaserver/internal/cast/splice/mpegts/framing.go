package mpegts

import (
	"slices"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/splice"
)

// Framing is the MPEG transport stream as splice reads it.
type Framing struct{}

var _ splice.Framing = Framing{}

func (Framing) Scanner() splice.Scanner { return &scanner{pmt: -1, video: -1, sound: -1} }

func (Framing) Trimmer(tables []byte, before time.Duration) splice.Trimmer {
	t := &trimmer{before: before, sound: -1}
	t.learn(tables)
	return t
}

// scanner follows a transport stream's packets, resynchronizing on noise.
type scanner struct {
	read    int64
	partial []byte
	pmt     int
	video   int
	sound   int
	pat     []byte
	table   []byte
}

func (s *scanner) Scan(p []byte, found func(splice.Event)) {
	data := p
	if len(s.partial) > 0 {
		s.partial = append(s.partial, p...)
		data = s.partial
	}
	for len(data) >= packetSize {
		if data[0] != syncByte {
			skip := slices.Index(data[1:], syncByte)
			if skip < 0 {
				s.read += int64(len(data))
				data = nil
				break
			}
			s.read += int64(skip + 1)
			data = data[skip+1:]
			continue
		}
		s.packet(data[:packetSize], found)
		s.read += packetSize
		data = data[packetSize:]
	}
	s.partial = append(s.partial[:0], data...)
}

func (s *scanner) packet(pkt []byte, found func(splice.Event)) {
	pid := int(pkt[1]&0x1f)<<8 | int(pkt[2])
	start := pkt[1]&0x40 != 0
	switch {
	case pid == 0 && start:
		s.pat = slices.Clone(pkt)
		s.pmt = programMapPID(pkt)
	case pid == s.pmt && start:
		s.table = slices.Clone(pkt)
		s.video = streamPID(pkt, videoStreams)
		s.sound = streamPID(pkt, soundStreams)
	case pid == s.sound && start:
		if pts, ok := presentation(pkt); ok {
			found(splice.Event{Kind: splice.Sound, Offset: s.read, PTS: pts})
		}
	case pid == s.video && start:
		pts, ok := presentation(pkt)
		switch {
		case !ok:
		case !randomAccess(pkt) || s.pat == nil || s.table == nil:
			found(splice.Event{Kind: splice.Picture, Offset: s.read, PTS: pts})
		default:
			found(splice.Event{Kind: splice.Keyframe, Offset: s.read, PTS: pts, Tables: slices.Concat(s.pat, s.table)})
		}
	}
}
