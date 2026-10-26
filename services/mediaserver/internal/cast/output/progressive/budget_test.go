package progressive

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
)

func TestCompletedDeliveryPreservesSpoolFailure(t *testing.T) {
	spool, err := storage.NewSpoolWithLimit(filepath.Join(t.TempDir(), "stream"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	spool.CloseWrite(storage.ErrSpoolLimit)
	done := make(chan struct{})
	close(done)
	stream := ServeSpool(output.Opening{}, spool)
	stream.audience.completed = true
	if err := stream.Wait(t.Context(), done); !errors.Is(err, storage.ErrSpoolLimit) {
		t.Fatalf("delivery erased producer failure: %v", err)
	}
}

func newTestSpool(t *testing.T) *storage.Spool {
	t.Helper()
	s, err := storage.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
