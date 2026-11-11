package cast

import (
	"fmt"
	"testing"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestNativeIncompatibilityHasATranslatableFailureCode(t *testing.T) {
	if got := failureCode(fmt.Errorf("planning media: %w", media.ErrNativeIncompatible)); got != castorv1.FailureCode_FAILURE_CODE_NATIVE_INCOMPATIBLE {
		t.Fatalf("native failure code = %v", got)
	}
}

func TestLiveOptimizationStatusSurvivesLaterRevisions(t *testing.T) {
	s := newCast(t.Context(), "cast", nil, nil, nil, 0)
	s.fire(eventLendStream, func(*view) {})
	s.Attempting(1)
	s.Revising(recovery.OptimizeLive, "the source requires conversion")
	s.Attempting(2)
	first, _ := s.now.Load()
	if got := first.status.GetCasting(); !got.GetOptimizedLive() || got.GetRevision().GetAction() != castorv1.RecoveryAction_RECOVERY_ACTION_OPTIMIZE_LIVE {
		t.Fatalf("optimized live status = %v", first.status)
	}
	s.Revising(recovery.RelaxRead, "stalled")
	s.Attempting(3)
	latest, _ := s.now.Load()
	if !latest.status.GetCasting().GetOptimizedLive() || first.status.GetCasting().GetAttempt() != 2 {
		t.Fatal("a later revision lost the effective mode or mutated a previously published status")
	}
}
