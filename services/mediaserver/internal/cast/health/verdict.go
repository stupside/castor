package health

// Phase is how far an attempt got; past Playing a cast restarts only by resuming where the viewer was.
type Phase int

const (
	// Unstarted is an attempt that ended before anything was read.
	Unstarted Phase = iota
	// Reading is the source read landing media with no device pointed at anything yet; faults are revisable.
	Reading
	// Opening is the artifact a device will be pointed at being produced, judged on it rather than the read.
	Opening
	// Playing is the device holding the URL: the line a recovery does not cross.
	Playing
	// Delivered is the cast having run its course.
	Delivered
)

func (p Phase) String() string {
	switch p {
	case Reading:
		return "reading"
	case Opening:
		return "opening"
	case Playing:
		return "playing"
	case Delivered:
		return "delivered"
	default:
		return "unstarted"
	}
}

// Kind is the verdict a rule reaches about a cast.
type Kind int

const (
	// starting is nothing wrong and nothing proven.
	starting Kind = iota
	// ready is a buffer an encoder may read, or an artifact a device may fetch.
	ready
	// healthy is a cast in flight with nothing against it.
	healthy
	// Lean is a device about to outrun its read, which the watch holds.
	Lean
	// Replenished is a held device whose read is ahead again, which the watch lets go.
	Replenished
	// Undeliverable is a read under playback pace for the whole deliver budget.
	Undeliverable
	// Stalled is a producer silent for the stall window.
	Stalled
	// Starved is a device that took all the read had made, which was not all of the program.
	Starved
	// Dead is a producer that ended with nothing playable.
	Dead
	// Choked is a device whose link takes less than playback consumes.
	Choked
	// Roomy is a device that has run well at a rate lighter than the cast could weigh.
	Roomy
	// Unfetched is a device that accepted the URL and never fetched it.
	Unfetched
	// Outranked is a cast that played well on a link another outranks.
	Outranked
)

func (k Kind) String() string {
	switch k {
	case ready:
		return "ready"
	case healthy:
		return "healthy"
	case Lean:
		return "lean"
	case Replenished:
		return "replenished"
	case Undeliverable:
		return "undeliverable"
	case Stalled:
		return "stalled"
	case Starved:
		return "starved"
	case Dead:
		return "dead"
	case Choked:
		return "choked"
	case Roomy:
		return "roomy"
	case Unfetched:
		return "unfetched"
	case Outranked:
		return "outranked"
	default:
		return "starting"
	}
}

// action is what a verdict asks of the watch.
type action int

const (
	// keepWatching re-reads the facts on the next tick.
	keepWatching action = iota
	// open ends the wait successfully.
	open
	revise
	// abandon ends the cast with the verdict's reasoning and the measurements behind it.
	abandon
	// hold pauses the device; release lets it play again.
	hold
	release
	// lighten asks the cast to weigh less, lift to weigh more, while the device plays on.
	lighten
	lift
)

// verdict is the pair the action table is keyed on: what was judged, and in which phase.
type verdict struct {
	kind  Kind
	phase Phase
}

// actions is what each verdict asks for in each phase; while playing, only a source that gave out or a better link ends the attempt.
var actions = map[verdict]action{
	{kind: starting, phase: Reading}: keepWatching,
	{kind: starting, phase: Opening}: keepWatching,
	{kind: healthy, phase: Playing}:  keepWatching,

	{kind: ready, phase: Reading}: open,
	{kind: ready, phase: Opening}: open,

	{kind: Dead, phase: Reading}:          revise,
	{kind: Dead, phase: Opening}:          revise,
	{kind: Undeliverable, phase: Reading}: revise,
	{kind: Stalled, phase: Opening}:       revise,

	{kind: Lean, phase: Playing}:        hold,
	{kind: Replenished, phase: Playing}: release,

	{kind: Stalled, phase: Playing}:   abandon,
	{kind: Starved, phase: Playing}:   abandon,
	{kind: Choked, phase: Playing}:    lighten,
	{kind: Roomy, phase: Playing}:     lift,
	{kind: Unfetched, phase: Playing}: abandon,
	{kind: Outranked, phase: Playing}: revise,
}
