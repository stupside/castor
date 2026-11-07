package hls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage/diskbudget"
	"golang.org/x/sync/semaphore"
)

const SegmentDirectoryLimit int64 = 256 << 20
const segmentFileLimit = 64
const directoryCheckInterval = 100 * time.Millisecond

// Reserve reserves the output budget before its producer starts. The
// returned cleanup runs after the producer and HTTP delivery have stopped.
func Reserve(dir string) (func() error, error) {
	return reserveDirectory(dir, SegmentDirectoryLimit, diskbudget.Shared)
}

func reserveDirectory(dir string, amount int64, budget *semaphore.Weighted) (func() error, error) {
	if amount <= 0 || budget == nil || !budget.TryAcquire(amount) {
		return nil, diskbudget.ErrLimit
	}
	return sync.OnceValue(func() error {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		budget.Release(amount)
		return nil
	}), nil
}

// checkDirectory bounds enumeration as well as file count and bytes. HLS
// production is flat; unexpected directories or links are refused explicitly.
func checkDirectory(dir string, limit int64, files int) error {
	root, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	var size int64
	count := 0
	for {
		entries, err := root.ReadDir(32)
		for _, entry := range entries {
			count++
			if count > files {
				return fmt.Errorf("segment output exceeds %d files: %w", files, diskbudget.ErrLimit)
			}
			info, statErr := entry.Info()
			if errors.Is(statErr, fs.ErrNotExist) {
				continue
			}
			if statErr != nil {
				return statErr
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("unexpected segment output %q", entry.Name())
			}
			if info.Size() > limit-size {
				return fmt.Errorf("segment output exceeds %d bytes: %w", limit, diskbudget.ErrLimit)
			}
			size += info.Size()
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func monitorDirectory(ctx context.Context, dir string) error {
	return monitorDirectoryWithin(ctx, dir, SegmentDirectoryLimit, segmentFileLimit)
}

func monitorDirectoryWithin(ctx context.Context, dir string, limit int64, files int) error {
	ticker := time.NewTicker(directoryCheckInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := checkDirectory(dir, limit, files); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	return nil
}
