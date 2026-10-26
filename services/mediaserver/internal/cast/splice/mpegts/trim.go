package mpegts

import "time"

// trimmer drops the audio frames of a stream that open before a time, passing everything else on in whole packets.
type trimmer struct {
	before time.Duration
	sound  int
	// dropping is whether the frame in progress is being dropped.
	dropping bool
	// partial is the start of a packet the last bytes ended in the middle of.
	partial []byte
}

// learn reads the audio stream out of the tables a cursor begins with.
func (s *trimmer) learn(tables []byte) {
	for at := 0; at+packetSize <= len(tables); at += packetSize {
		if pid := streamPID(tables[at:at+packetSize], soundStreams); pid >= 0 {
			s.sound = pid
		}
	}
}

// Trim is p less the dropped frames, which may be less than p holds or more, since a packet can end in a later call.
func (s *trimmer) Trim(p []byte) []byte {
	data := append(s.partial, p...)
	var kept []byte
	for len(data) >= packetSize {
		pkt := data[:packetSize]
		data = data[packetSize:]
		if pkt[0] == syncByte && s.sound >= 0 && int(pkt[1]&0x1f)<<8|int(pkt[2]) == s.sound {
			if pkt[1]&0x40 != 0 {
				pts, ok := presentation(pkt)
				s.dropping = ok && pts < s.before
			}
			if s.dropping {
				continue
			}
		}
		kept = append(kept, pkt...)
	}
	s.partial = append([]byte(nil), data...)
	return kept
}
