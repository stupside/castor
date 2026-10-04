package source

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
