package airplay

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Pairings keeps the credentials castor paired with, by receiver deviceid, in one file only castor's user reads.
type Pairings struct {
	path string
	mu   sync.Mutex
}

// NewPairings keeps pairings at path; empty keeps them in castor's directory under the user's configuration directory.
func NewPairings(path string) *Pairings {
	if path == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			path = filepath.Join(dir, "castor", "airplay-pairings.json")
		}
	}
	return &Pairings{path: path}
}

type pairingFile struct {
	Receivers map[string]Credentials `json:"receivers"`
}

func receiverKey(deviceID string) string { return strings.ToUpper(strings.TrimSpace(deviceID)) }

func (p *Pairings) read() (pairingFile, error) {
	f := pairingFile{Receivers: map[string]Credentials{}}
	if p.path == "" {
		return f, nil
	}
	data, err := os.ReadFile(p.path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("reading AirPlay pairings: %w", err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("reading AirPlay pairings %s: %w", p.path, err)
	}
	if f.Receivers == nil {
		f.Receivers = map[string]Credentials{}
	}
	return f, nil
}

// Credentials are the ones castor paired with deviceID, false where it never did.
func (p *Pairings) Credentials(deviceID string) (Credentials, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := p.read()
	if err != nil {
		return Credentials{}, false, err
	}
	c, ok := f.Receivers[receiverKey(deviceID)]
	return c, ok && c.valid(), nil
}

// Save keeps c for deviceID.
func (p *Pairings) Save(deviceID string, c Credentials) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.path == "" {
		return errors.New("castor has nowhere to keep AirPlay pairings: no user configuration directory")
	}
	f, err := p.read()
	if err != nil {
		return err
	}
	f.Receivers[receiverKey(deviceID)] = c
	return p.write(f)
}

// write replaces the file whole, so a crash leaves the previous one, readable by castor's user only.
func (p *Pairings) write(f pairingFile) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return fmt.Errorf("keeping AirPlay pairings: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p.path), ".airplay-pairings-*")
	if err != nil {
		return fmt.Errorf("keeping AirPlay pairings: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		_ = tmp.Close()
		return fmt.Errorf("keeping AirPlay pairings: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("keeping AirPlay pairings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("keeping AirPlay pairings: %w", err)
	}
	if err := os.Rename(tmp.Name(), p.path); err != nil {
		return fmt.Errorf("keeping AirPlay pairings: %w", err)
	}
	return nil
}

// Forget drops what castor holds for deviceID, which the receiver no longer accepts.
func (p *Pairings) Forget(deviceID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := p.read()
	if err != nil {
		return err
	}
	if _, ok := f.Receivers[receiverKey(deviceID)]; !ok {
		return nil
	}
	delete(f.Receivers, receiverKey(deviceID))
	return p.write(f)
}
