package splice

import "time"

// Framing is all splice needs to know of a container to cut it: where its keyframes and its sound open, and how to drop sound from a stream carried on.
type Framing interface {
	Scanner() Scanner
	// Trimmer drops the sound that opens before a time from a stream a reader starts at a cut of, given the tables the reader begins with.
	Trimmer(tables []byte, before time.Duration) Trimmer
}

// Scanner reads a stream in the order it is written, holding what a write leaves unfinished, and reports what opens in it.
type Scanner interface {
	Scan(p []byte, found func(Event))
}

// Trimmer passes a stream on less the sound it drops, whole frames at a time.
type Trimmer interface {
	Trim(p []byte) []byte
}

// Kind is what opens at an event.
type Kind int

const (
	// Picture is a picture that is not a keyframe.
	Picture Kind = iota
	Keyframe
	Sound
)

// Event is something that opens in the stream, at a byte and a time.
type Event struct {
	Kind   Kind
	Offset int64
	PTS    time.Duration
	// Tables are what a reader starting at a keyframe needs first, as the stream said them there.
	Tables []byte
}
