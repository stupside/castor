// Package airplay casts video to AirPlay receivers: Apple TVs through /play (video v1), third-party TVs through
// a remote control stream (video v2). The protocol logic follows pyatv (MIT, https://github.com/postlund/pyatv).
package airplay

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/grandcat/zeroconf"
	"howett.net/plist"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

const (
	defaultPort = 7000
	serviceType = "_airplay._tcp"

	// pairingName is how castor appears among the receiver's paired devices.
	pairingName = "Castor"
)

// Config is where castor keeps the pairings it makes.
type Config struct {
	// Pairings is the file castor keeps receiver credentials in; empty keeps them under the user's configuration directory.
	Pairings string `yaml:"pairings"`
}

// Family is the AirPlay strategy, keeping its pairings in Pairings.
type Family struct {
	Pairings *Pairings
}

// familyType is the type the contract names this family by.
const familyType device.Type = "airplay"

func (Family) Type() device.Type { return familyType }

var (
	_ device.Family    = Family{}
	_ device.Describer = Family{}
	_ device.Pairer    = Family{}
)

// Capabilities are the family's own, so describing a receiver neither dials nor wakes it.
func (Family) Capabilities(context.Context, device.Info) (*mediav1.Capabilities, error) {
	return capabilities(), nil
}

// Locate passes the address straight through, Connect already accepting both of its forms.
func (Family) Locate(_ context.Context, address string) (string, error) { return address, nil }

// describe reads what a receiver says of itself before any pairing: its /info, else an AirPlay 1 receiver's /server-info.
func describe(ctx context.Context, l *link, info device.Info) (receiver, error) {
	r := receiver{address: l.address, name: cmp.Or(info.Name, info.Address), deviceID: info.ID}
	var answer struct {
		DeviceID    string `plist:"deviceID"`
		LowDeviceID string `plist:"deviceid"`
		Features    uint64 `plist:"features"`
		StatusFlags uint64 `plist:"statusFlags"`
		Model       string `plist:"model"`
		Name        string `plist:"name"`
	}
	read := func(uri, proto string) bool {
		resp, err := l.exchange(ctx, request{method: "GET", uri: uri, proto: proto})
		if err != nil || resp.code != 200 || len(resp.body) == 0 {
			return false
		}
		_, err = plist.Unmarshal(resp.body, &answer)
		return err == nil
	}
	if !read("/info", protoRTSP) && !read("/server-info", protoHTTP) {
		return r, errors.New("the receiver does not say what it plays (no /info)")
	}
	r.features, r.status, r.model = features(answer.Features), answer.StatusFlags, answer.Model
	r.deviceID = cmp.Or(answer.DeviceID, answer.LowDeviceID, r.deviceID)
	if info.Name == "" && answer.Name != "" {
		r.name = answer.Name
	}
	return r, nil
}

func (f Family) Connect(ctx context.Context, info device.Info) (device.Device, error) {
	l, err := dial(ctx, device.WithPort(info.Address, strconv.Itoa(defaultPort)))
	if err != nil {
		return nil, fmt.Errorf("connecting to the AirPlay receiver: %w", err)
	}
	r, err := describe(ctx, l, info)
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	if r.features.videoMethod() == methodNone || unsupportedModel.MatchString(r.model) {
		_ = l.Close()
		return nil, fmt.Errorf("%s: %w", r.name, errNoVideo)
	}
	s := &session{r: r}
	if creds, ok, err := f.Pairings.Credentials(r.deviceID); err != nil {
		_ = l.Close()
		return nil, err
	} else if ok {
		s.creds = &creds
	} else if r.status&(statusPairingRequired|statusPINRequired) != 0 {
		_ = l.Close()
		return nil, fmt.Errorf("%s asks castor to pair with it first: %w", r.name, device.ErrPairingRequired)
	} else {
		s.transient = r.features.transient()
	}
	secret, err := s.open(ctx, l)
	if err != nil {
		_ = l.Close()
		if errors.Is(err, errRejected) {
			// The receiver forgot castor: a stale pairing must not stand in the way of a new one.
			if forgetErr := f.Pairings.Forget(r.deviceID); forgetErr != nil {
				slog.WarnContext(ctx, "forgetting a rejected AirPlay pairing", "device", r.name, "error", forgetErr)
			}
		}
		return nil, err
	}
	s.spare, s.spareSecret = l, secret
	slog.DebugContext(ctx, "airplay receiver", "device", r.name, "method", r.features.videoMethod().String(), "paired", s.creds != nil)
	return s, nil
}

// BeginPairing asks the receiver to show a PIN, which Finish proves.
func (f Family) BeginPairing(ctx context.Context, info device.Info) (device.Pairing, error) {
	l, err := dial(ctx, device.WithPort(info.Address, strconv.Itoa(defaultPort)))
	if err != nil {
		return nil, fmt.Errorf("connecting to the AirPlay receiver: %w", err)
	}
	r, err := describe(ctx, l, info)
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	if r.deviceID == "" {
		_ = l.Close()
		return nil, errors.New("the receiver does not say its device id, which castor keeps its pairing by")
	}
	setup, err := beginPairSetup(ctx, l)
	if err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("asking %s for a PIN: %w", r.name, err)
	}
	return &pairing{l: l, setup: setup, deviceID: r.deviceID, store: f.Pairings}, nil
}

// pairing is one PIN pairing a client completes or abandons.
type pairing struct {
	l        *link
	setup    *pairSetup
	deviceID string
	store    *Pairings
	once     sync.Once
}

func (p *pairing) Finish(ctx context.Context, pin string) error {
	defer p.Close()
	creds, err := p.setup.finish(ctx, strings.TrimSpace(pin), pairingName)
	if err != nil {
		if errors.Is(err, errWrongPIN) {
			return fmt.Errorf("%w: %w", device.ErrWrongPIN, err)
		}
		return err
	}
	return p.store.Save(p.deviceID, creds)
}

func (p *pairing) Close() error {
	p.once.Do(func() { _ = p.l.Close() })
	return nil
}

// Discover browses mDNS (_airplay._tcp) until ctx expires, keeping receivers that play video.
func (Family) Discover(ctx context.Context) []device.Info {
	interfaces, err := net.Interfaces()
	if err != nil {
		slog.WarnContext(ctx, "airplay interfaces", "error", err)
		return nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var devices []device.Info
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		wg.Go(func() {
			resolver, err := zeroconf.NewResolver(zeroconf.SelectIPTraffic(zeroconf.IPv4), zeroconf.SelectIfaces([]net.Interface{iface}))
			if err != nil {
				slog.DebugContext(ctx, "airplay discovery", "interface", iface.Name, "error", err)
				return
			}
			entries := make(chan *zeroconf.ServiceEntry)
			if err := resolver.Browse(ctx, serviceType, "local", entries); err != nil {
				slog.DebugContext(ctx, "airplay discovery", "interface", iface.Name, "error", err)
				return
			}
			// The resolver closes entries once ctx ends.
			for entry := range entries {
				if found, ok := announced(entry); ok {
					mu.Lock()
					devices = append(devices, found)
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	return devices
}

// announced is the receiver entry announces, false for one that plays no video castor can hand it.
func announced(entry *zeroconf.ServiceEntry) (device.Info, bool) {
	txt := map[string]string{}
	for _, record := range entry.Text {
		k, v, _ := strings.Cut(record, "=")
		txt[strings.ToLower(k)] = v
	}
	f, err := parseFeatures(cmp.Or(txt["features"], txt["ft"], "0x0"))
	if err != nil {
		return device.Info{}, false
	}
	if fex, err := parseFeaturesEx(txt["fex"]); txt["fex"] != "" && err == nil {
		f |= fex
	}
	if f.videoMethod() == methodNone || unsupportedModel.MatchString(txt["model"]) || txt["deviceid"] == "" {
		return device.Info{}, false
	}
	var host string
	switch {
	case len(entry.AddrIPv4) > 0:
		host = entry.AddrIPv4[0].String()
	default:
		return device.Info{}, false
	}
	address := host
	if entry.Port > 0 && entry.Port != defaultPort {
		address = net.JoinHostPort(host, strconv.Itoa(entry.Port))
	}
	return device.Info{ID: receiverKey(txt["deviceid"]), Name: unescapeLabel(entry.Instance), Type: familyType, Address: address}, true
}

// unescapeLabel reads a DNS label as presentation format writes it: \DDD is a decimal byte, \X the character X.
func unescapeLabel(label string) string {
	var b strings.Builder
	for i := 0; i < len(label); i++ {
		if label[i] != '\\' || i+1 == len(label) {
			b.WriteByte(label[i])
			continue
		}
		if i+3 < len(label) {
			if n, err := strconv.ParseUint(label[i+1:i+4], 10, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(label[i+1])
		i++
	}
	return b.String()
}
