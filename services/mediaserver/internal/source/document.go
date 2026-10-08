package source

import (
	"fmt"
	"io"
)

// DocumentLimit is far above any real playlist, manifest, init section or key, and far below a film served in its place.
const DocumentLimit = 16 << 20

// ReadDocument reads r to its end, refusing more than DocumentLimit bytes.
func ReadDocument(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, DocumentLimit+1))
	if err == nil && len(b) > DocumentLimit {
		return nil, fmt.Errorf("larger than %d bytes", DocumentLimit)
	}
	return b, err
}

// Ladder is what a body says of the renditions it offers.
type Ladder int

const (
	// LadderUnknown is the zero value because reading the body is best-effort.
	LadderUnknown Ladder = iota
	// LadderMultivariant is a document advertising renditions: a master.
	LadderMultivariant
	// LadderSole is a playlist advertising no renditions: it IS the rendition.
	LadderSole
)

func (l Ladder) String() string {
	switch l {
	case LadderMultivariant:
		return "multivariant"
	case LadderSole:
		return "sole"
	default:
		return "unknown"
	}
}
