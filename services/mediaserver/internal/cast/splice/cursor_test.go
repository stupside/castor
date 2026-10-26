package splice

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"
)

func spoolOf(t *testing.T, data []byte) *storage.Spool {
	t.Helper()
	sp, err := storage.NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sp.Close() })
	if _, err := sp.Write(data); err != nil {
		t.Fatal(err)
	}
	return sp
}

func TestACursorStartingAtACutReadsTheTablesThenTheBufferFromThere(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789"), 100)
	c, err := Open(t.Context(), spoolOf(t, data), 300, []byte("TABLES"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.Stop(500) {
		t.Fatal("a cursor that had read nothing refused to stop at 500")
	}
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if want := append([]byte("TABLES"), data[300:500]...); !bytes.Equal(got, want) {
		t.Errorf("read %d bytes, want the tables then bytes 300 to 500", len(got))
	}
}

func TestACursorCannotBeStoppedBehindWhatItHasRead(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 1000)
	c, err := Open(t.Context(), spoolOf(t, data), 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := io.ReadFull(c, make([]byte, 400)); err != nil {
		t.Fatal(err)
	}
	if c.Offset() != 400 {
		t.Errorf("offset = %d, want 400", c.Offset())
	}
	if c.Stop(399) {
		t.Error("a cursor agreed to stop behind what it had read")
	}
	if !c.Stop(400) {
		t.Error("a cursor refused to stop where it stands")
	}
	if n, err := c.Read(make([]byte, 10)); n != 0 || err != io.EOF {
		t.Errorf("read past the limit = %d, %v, want end of file", n, err)
	}
}

func TestACursorWithoutALimitFollowsTheBufferToItsEnd(t *testing.T) {
	sp := spoolOf(t, []byte("abc"))
	c, err := Open(t.Context(), sp, 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sp.CloseWrite(nil)
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "bc" {
		t.Errorf("read %q, %v, want bc", got, err)
	}
}
