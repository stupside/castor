// Package compose decides the shape of a cast: handed to a device that fetches for itself, forwarded byte for byte, remuxed on the fly, or read once and served.
package compose

// Kind is what castor does with the bytes of one cast.
type Kind string

const (
	// Handoff: the device fetches the source itself.
	Handoff Kind = "handoff"
	// Forward: castor forwards the source file unchanged to a device that cannot fetch it itself.
	Forward Kind = "forward"
	// Remux: castor reads the source and serves what it produced.
	Remux Kind = "remux"
	// ReadOnce: castor buffers the source locally and serves what it produced from the buffer.
	ReadOnce Kind = "read-once"
)

// For is the kind of cast shape s calls for; a remux answers a device that rejects the source or whose headers do not match.
func For(s Shape) Kind {
	switch {
	case !s.Device.SelfFetch && s.forwards():
		return Forward
	case !s.Device.SelfFetch:
		return ReadOnce
	case s.handoff():
		return Handoff
	default:
		return Remux
	}
}

// Original is a kind whose device plays the source's own bytes, every track and signal the file carries.
func (k Kind) Original() bool { return k == Handoff || k == Forward }
