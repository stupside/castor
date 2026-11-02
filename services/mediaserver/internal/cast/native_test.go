package cast

import (
	"fmt"
	"testing"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestNativeIncompatibilityHasATranslatableFailureCode(t *testing.T) {
	if got := failureCode(fmt.Errorf("planning media: %w", media.ErrNativeIncompatible)); got != castorv1.FailureCode_FAILURE_CODE_NATIVE_INCOMPATIBLE {
		t.Fatalf("native failure code = %v", got)
	}
}
