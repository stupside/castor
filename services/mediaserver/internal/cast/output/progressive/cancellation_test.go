package progressive

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestCancelledWriterRetainsOnlyIndependentFailures(t *testing.T) {
	for _, fails := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		format, _ := container.For(media.MPEGTS)
		stream, err := NewStream(output.Opening{Format: format}, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stream.Close() })
		read, write := io.Pipe()
		t.Cleanup(func() { _ = read.Close(); _ = write.Close() })
		done := make(chan error, 1)
		go func() { done <- stream.Write(ctx, read) }()
		var failure error
		if fails {
			failure = errors.New("storage failed")
		}
		cancel()
		_ = write.CloseWithError(errors.Join(os.ErrClosed, failure))
		err = <-done
		if failure == nil && (err != nil || errors.Is(stream.spool.Err(), os.ErrClosed)) {
			t.Fatalf("canceled pipe became a producer failure: write=%v spool=%v", err, stream.spool.Err())
		}
		if failure == nil && !errors.Is(stream.spool.Err(), context.Canceled) {
			t.Fatalf("truncated spool reported success: %v", stream.spool.Err())
		}
		if failure != nil && (!errors.Is(err, failure) || !errors.Is(stream.spool.Err(), failure)) {
			t.Fatalf("lost independent failure: write=%v spool=%v", err, stream.spool.Err())
		}
	}
}
