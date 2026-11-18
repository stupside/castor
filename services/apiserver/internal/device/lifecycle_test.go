package device

import "testing"

func TestAPlaybackStartsPlaysAndEndsOrIsRefusedOrFails(t *testing.T) {
	type step func(*Lifecycle)
	played := func(l *Lifecycle) { l.Played(t.Context()) }
	stopped := func(l *Lifecycle) { l.Stopped(t.Context()) }
	refused := func(l *Lifecycle) { l.Refused(t.Context()) }
	failed := func(l *Lifecycle) { l.Failed(t.Context()) }
	for _, tt := range []struct {
		name                           string
		steps                          []step
		started, over, wasRefused, has bool
	}{
		{"handed over", nil, false, false, false, false},
		{"idle before it played is no end", []step{stopped}, false, false, false, false},
		{"playing", []step{played}, true, false, false, false},
		{"paused or playing again is still playing", []step{played, played}, true, false, false, false},
		{"idle after it played ends it", []step{played, stopped}, true, true, false, false},
		{"refused before it played", []step{refused}, false, true, true, false},
		{"refused after it played changes nothing", []step{played, refused}, true, false, false, false},
		{"failing before it played is no failure", []step{failed}, false, false, false, false},
		{"failing while it plays", []step{played, failed}, true, true, false, true},
		{"nothing follows an end", []step{played, stopped, played, refused, failed}, true, true, false, false},
		{"nothing follows a refusal", []step{refused, played, stopped}, false, true, true, false},
		{"nothing follows a failure", []step{played, failed, stopped, played}, true, true, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			l := NewLifecycle("test")
			for _, s := range tt.steps {
				s(l)
			}
			if l.Started() != tt.started || l.Over() != tt.over || l.WasRefused() != tt.wasRefused || l.HasFailed() != tt.has {
				t.Errorf("started=%v over=%v refused=%v failed=%v, want %v %v %v %v", l.Started(), l.Over(), l.WasRefused(), l.HasFailed(), tt.started, tt.over, tt.wasRefused, tt.has)
			}
		})
	}
}
