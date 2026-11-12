package hls

import (
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

func (s *segmentStore) landed() (int64, error) {
	info, err := os.Stat(s.playlist)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *segmentStore) Close() error { return s.root.Close() }
