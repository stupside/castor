package cast

import (
	"testing"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
)

func TestEveryRecoveryActionIsNamedByTheContract(t *testing.T) {
	for action, want := range map[recovery.Action]castorv1.RecoveryAction{
		recovery.SwitchCandidate: castorv1.RecoveryAction_RECOVERY_ACTION_SWITCH_CANDIDATE,
		recovery.DecodeAxis:      castorv1.RecoveryAction_RECOVERY_ACTION_DECODE_AXIS,
		recovery.RelaxRead:       castorv1.RecoveryAction_RECOVERY_ACTION_RELAX_READ,
		recovery.ServeInstead:    castorv1.RecoveryAction_RECOVERY_ACTION_SERVE_INSTEAD,
		recovery.Stay:            castorv1.RecoveryAction_RECOVERY_ACTION_STAY,
		recovery.SoftwareEncode:  castorv1.RecoveryAction_RECOVERY_ACTION_SOFTWARE_ENCODE,
	} {
		if got := recoveryAction(action); got != want {
			t.Errorf("recoveryAction(%q) = %s, want %s", action, got, want)
		}
	}
}
