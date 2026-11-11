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

func (s *segmentStore) rootPlaylist() string { return filepath.Base(s.playlist) }

func openSegmentStore(dir, playlist string) (*segmentStore, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening the HLS directory: %w", err)
	}
	return &segmentStore{root: root, dir: dir, playlist: filepath.Join(dir, playlist)}, nil
}

func (s *segmentStore) landed() (int64, error) {
	if s.captions != nil {
		s.captions.mu.Lock()
		defer s.captions.mu.Unlock()
		video, _, err := s.captions.playlists()
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errCaptionsPending) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		return int64(len(video)), nil
	}
	info, err := os.Stat(s.playlist)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// liveStart is how many fragments a rolling playlist lists before a device is handed it: a live player starts that many target durations behind the newest.
const liveStart = 3

// rollingLanded holds a rolling playlist back until a live player has fragments to start behind its edge; one that has ended is whole.
func (s *segmentStore) rollingLanded() (int64, error) {
	body, err := os.ReadFile(s.playlist)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if bytes.Count(body, []byte("#EXTINF:")) < liveStart && !bytes.Contains(body, []byte("#EXT-X-ENDLIST")) {
		return 0, nil
	}
	return int64(len(body)), nil
}

func (s *segmentStore) Close() error { return s.root.Close() }
