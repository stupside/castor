// Package mpegts is how splice reads an MPEG transport stream: its packets, its tables and its timestamps.
package mpegts

import (
	"slices"
	"time"
)

const (
	packetSize = 188
	syncByte   = 0x47
)

// payload is where a packet's payload starts, or -1 when it has none.
func payload(pkt []byte) int {
	switch (pkt[3] >> 4) & 3 {
	case 1:
		return 4
	case 3:
		if at := 5 + int(pkt[4]); at < packetSize {
			return at
		}
	}
	return -1
}

// section is the table a packet opens: the bytes after its pointer field.
func section(pkt []byte) []byte {
	at := payload(pkt)
	if at < 0 || at >= packetSize {
		return nil
	}
	at += 1 + int(pkt[at])
	if at >= packetSize {
		return nil
	}
	return pkt[at:]
}

// programMapPID is where the first program's map is, or -1.
func programMapPID(pkt []byte) int {
	s := section(pkt)
	if len(s) < 12 || s[0] != 0x00 {
		return -1
	}
	end := min(3+(int(s[1]&0x0f)<<8|int(s[2])), len(s)) - 4
	for i := 8; i+4 <= end; i += 4 {
		if number := int(s[i])<<8 | int(s[i+1]); number != 0 {
			return int(s[i+2]&0x1f)<<8 | int(s[i+3])
		}
	}
	return -1
}

// videoStreams and soundStreams are the program map stream types of the codecs castor muxes.
var (
	videoStreams = []byte{0x01, 0x02, 0x10, 0x1b, 0x24, 0x42}
	soundStreams = []byte{0x03, 0x04, 0x0f, 0x11, 0x81, 0x87}
)

// streamPID is the first stream of one of types a program map lists, or -1.
func streamPID(pkt []byte, types []byte) int {
	s := section(pkt)
	if len(s) < 16 || s[0] != 0x02 {
		return -1
	}
	end := min(3+(int(s[1]&0x0f)<<8|int(s[2])), len(s)) - 4
	for i := 12 + (int(s[10]&0x0f)<<8 | int(s[11])); i+5 <= end; i += 5 + (int(s[i+3]&0x0f)<<8 | int(s[i+4])) {
		if slices.Contains(types, s[i]) {
			return int(s[i+1]&0x1f)<<8 | int(s[i+2])
		}
	}
	return -1
}

// randomAccess reports that a packet's adaptation field marks a point a decoder can start at.
func randomAccess(pkt []byte) bool {
	return (pkt[3]>>4)&2 != 0 && pkt[4] > 0 && pkt[5]&0x40 != 0
}

// presentation is the PTS of the PES packet a transport packet opens.
func presentation(pkt []byte) (time.Duration, bool) {
	at := payload(pkt)
	if at < 0 || at+14 > packetSize || pkt[at] != 0 || pkt[at+1] != 0 || pkt[at+2] != 1 {
		return 0, false
	}
	h := pkt[at:]
	if h[7]&0x80 == 0 {
		return 0, false
	}
	ticks := int64(h[9]>>1&7)<<30 | int64(h[10])<<22 | int64(h[11]>>1)<<15 | int64(h[12])<<7 | int64(h[13]>>1)
	return time.Duration(ticks) * time.Second / 90000, true
}
