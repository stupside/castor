package dlna

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

func (s *session) Position(ctx context.Context) (device.Playhead, error) {
	ctx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	var response struct{ RelTime string }
	if err := s.transport.SOAPClient.PerformActionCtx(ctx, s.transport.Service.ServiceType,
		"GetPositionInfo", &struct{ InstanceID string }{"0"}, &response); err != nil {
		return device.Playhead{}, fmt.Errorf("reading the playback position: %w", err)
	}
	at, err := parseClock(response.RelTime)
	// Many renderers answer NOT_IMPLEMENTED, or a 0:00:00 that never moves, while they play.
	if err != nil || at == 0 && !s.ticked.Load() {
		return device.Playhead{}, device.ErrNoPosition
	}
	s.ticked.Store(true)
	info, err := s.transportInfo(ctx)
	if err != nil {
		return device.Playhead{}, fmt.Errorf("reading the transport state: %w", err)
	}
	return device.Playhead{At: at, Paused: info.CurrentTransportState == "PAUSED_PLAYBACK"}, nil
}

// parseClock reads H+:MM:SS[.F+], the clock AVTransport states positions in.
func parseClock(s string) (time.Duration, error) {
	h, rest, ok1 := strings.Cut(strings.TrimSpace(s), ":")
	m, sec, ok2 := strings.Cut(rest, ":")
	whole, frac, _ := strings.Cut(sec, ".")
	hours, err1 := strconv.ParseUint(h, 10, 32)
	minutes, err2 := strconv.ParseUint(m, 10, 8)
	seconds, err3 := strconv.ParseUint(whole, 10, 8)
	if !ok1 || !ok2 || err1 != nil || err2 != nil || err3 != nil || minutes > 59 || seconds > 59 || strings.Trim(frac, "0123456789") != "" {
		return 0, fmt.Errorf("the position %q is not H:MM:SS", s)
	}
	at := time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second
	if frac != "" {
		f, _ := strconv.ParseFloat("0."+frac, 64)
		at += (time.Duration(f*1000+0.5) * time.Millisecond)
	}
	return at, nil
}
