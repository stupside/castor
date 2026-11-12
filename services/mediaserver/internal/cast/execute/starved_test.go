package execute

import (
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// TestADeviceThatEndsHavingTakenEverythingTheUnfinishedReadMadeIsStarved is told apart from one that stopped with media still to come.
func TestADeviceThatEndsHavingTakenEverythingTheUnfinishedReadMadeIsStarved(t *testing.T) {
	sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 64<<10)
	if _, err := sp.Write(body); err != nil {
		t.Fatal(err)
	}
	madeBytes := int64(len(body))
	progress := func() media.Progress { return media.Progress{Position: time.Minute, Bytes: madeBytes} }
	selected, err := testOutputs().Select(chromecastLike(media.MPEGTS), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	srv := selected.Plan.(output.Relayer).ServeSpool(output.Opening{
		Format:        selected.Plan.Format(),
		WriteDeadline: time.Minute,
	}, sp)
	pub, err := (loopback{}).Publish(t.Context(), srv.Handler())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close(); _ = srv.Close(); sp.CloseWrite(nil); _ = sp.Close() })

	resp, err := http.Get(pub.URL(srv.Path()).String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if _, err := io.ReadFull(resp.Body, make([]byte, len(body))); err != nil {
		t.Fatal(err)
	}

	read := &pull{done: make(chan struct{})}
	feedOf := func(r *pull) feed { return feed{buffered: &buffered{reader: r}} }
	d := delivery{sink: audienceOutput{Output: srv}, output: progressProducer{pull: read, progress: progress}}
	// Receiving the socket's bytes can precede the server recording its completed write.
	streamed := srv.Audience()
	deadline := time.Now().Add(time.Second)
	for handed, _ := streamed.Handed(); handed < madeBytes; handed, _ = streamed.Handed() {
		if time.Now().After(deadline) {
			t.Fatalf("server recorded %d handed bytes, want %d", handed, madeBytes)
		}
		time.Sleep(time.Millisecond)
	}

	if _, _, ok := starved(feedOf(read), d); !ok {
		t.Error("a device that took all the read had made, while the read still ran, was not starved")
	}

	madeBytes = int64(len(body)) * 2
	if _, _, ok := starved(feedOf(read), d); ok {
		t.Error("a device that stopped with media still waiting for it was called starved")
	}

	madeBytes = int64(len(body))
	close(read.done)
	if _, _, ok := starved(feedOf(read), d); ok {
		t.Error("a device that took everything from a read that had finished was called starved")
	}
}

type progressProducer struct {
	*pull
	progress func() media.Progress
}

func (p progressProducer) Progress() media.Progress { return p.progress() }

// Expose only the output contract, without progressive's concrete Handed method.
// Starvation detection must use Audience, as any protocol adapter can provide it.
type audienceOutput struct{ output.Output }
