package expect

import (
	"fmt"

	"github.com/stupside/castor/e2e/judge"
)

// Smallest holds that the picture ended on the origin's smallest rung, not on a smaller picture castor made.
type Smallest struct{}

func (Smallest) Name() string { return "smallest" }

func (Smallest) Judge(e judge.Evidence) []string {
	p, smallest := e.Received.Played, e.Origin.Stream.Heights[0]
	return judge.FailIf(p.Last != smallest, fmt.Sprintf("the picture ended %dp, want the origin's smallest rung of %dp", p.Last, smallest))
}
