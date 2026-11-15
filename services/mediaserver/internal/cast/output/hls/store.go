package hls

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// segmentStore owns access to artifacts and the optional caption rendition.
// Production and HTTP requests borrow it until both have stopped.
type segmentStore struct {
	root     *os.Root
	dir      string
	playlist string
	captions *captionDelivery
}

func openSegmentStore(dir, playlist string) (*segmentStore, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening the HLS directory: %w", err)
	}
	return &segmentStore{root: root, dir: dir, playlist: filepath.Join(dir, playlist)}, nil
}

// startFragments is how far behind the end of a playlist that goes on a player starts; handed fewer, it stalls at the edge.
const startFragments = 3

// startable is a playlist a device can start on: it lists startFragments or has ended.
func startable(playlist []byte) bool {
	return bytes.Count(playlist, []byte("#EXTINF:")) >= startFragments || bytes.Contains(playlist, []byte("#EXT-X-ENDLIST"))
}

func (s *segmentStore) landed() (int64, error) {
	playlist, err := os.ReadFile(s.playlist)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !startable(playlist) {
		return 0, nil
	}
	return int64(len(playlist)), nil
}

func (s *segmentStore) Close() error { return s.root.Close() }
