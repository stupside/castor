package roku

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

// session is a Roku driven over ECP, playing through the channel it launches.
type session struct {
	ecp   *url.URL
	appID string
	name  string
	hc    *http.Client
}

var _ device.Device = (*session)(nil)

// get reads at most limit bytes of an ECP answer, refusing any status but 200.
func get(ctx context.Context, hc *http.Client, u *url.URL, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u.Path, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (s *session) Play(ctx context.Context, playback device.Playback) error {
	var format string
	switch playback.Container {
	case mediav1.Container_CONTAINER_MP4:
		format = "mp4"
	case mediav1.Container_CONTAINER_MKV:
		format = "mkv"
	case mediav1.Container_CONTAINER_HLS:
		format = "hls"
	default:
		return fmt.Errorf("roku does not support container %v", playback.Container)
	}
	q := url.Values{}
	q.Set(paramURL, playback.URL.String())
	q.Set(paramFormat, format)

	u := s.ecp.JoinPath("launch", s.appID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("launching roku channel: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("roku launch: %s (channel %q installed?)", resp.Status, s.appID)
	}
	return nil
}

// statePause is what /query/media-player reports of a paused player.
const statePause = "pause"

func playing(state string) bool { return state == "play" || state == "buffer" || state == "startup" }

func over(state string) bool { return state == "close" || state == "stop" || state == "none" }

// Pause holds the player paused; ECP has one Play key for both, so it presses it only when the player is playing.
func (s *session) Pause(ctx context.Context) error { return s.hold(ctx, true) }

func (s *session) Resume(ctx context.Context) error { return s.hold(ctx, false) }

func (s *session) hold(ctx context.Context, paused bool) error {
	player, err := s.player(ctx)
	if err != nil {
		return err
	}
	state := player.State
	switch {
	case paused && state == statePause, !paused && playing(state):
		return nil
	case paused && playing(state), !paused && state == statePause:
		return s.keypress(ctx, "Play")
	}
	return fmt.Errorf("roku player is %q, with nothing to pause or resume", state)
}

func (s *session) keypress(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.ecp.JoinPath("keypress", key).String(), nil)
	if err != nil {
		return err
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("roku keypress %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("roku keypress %s: %s", key, resp.Status)
	}
	return nil
}

// Stop leaves the channel for the home screen, which ends its playback.
func (s *session) Stop(ctx context.Context) error { return s.keypress(ctx, "Home") }

const startupTimeout = 30 * time.Second

const mediaPlayerQuery = "/query/media-player"

type mediaPlayer struct {
	State        string `xml:"state,attr"`
	Error        bool   `xml:"error,attr"`
	ErrorMessage string `xml:"error-message"`
	ErrorCode    string `xml:"error-code"`
	Position     string `xml:"position"`
	Plugin       struct {
		ID string `xml:"id,attr"`
	} `xml:"plugin"`
}

// player is castor's media player as reported, a playback error once it reports one.
func (s *session) player(ctx context.Context) (mediaPlayer, error) {
	var player mediaPlayer
	body, err := get(ctx, s.hc, s.ecp.JoinPath(mediaPlayerQuery), 1<<16)
	if err != nil {
		return player, err
	}
	if err := xml.Unmarshal(body, &player); err != nil {
		return player, fmt.Errorf("decoding roku player state: %w", err)
	}
	// Another channel holding the player means castor's closed, whatever that channel's own playback reports.
	if player.Plugin.ID != "" && player.Plugin.ID != s.appID {
		return mediaPlayer{State: "close"}, nil
	}
	if player.Error {
		return player, &device.PlaybackError{Err: fmt.Errorf("roku playback failed (code %s): %s", player.ErrorCode, player.ErrorMessage)}
	}
	return player, nil
}

// Position is the player's, stated in milliseconds, while it holds castor's media.
func (s *session) Position(ctx context.Context) (device.Playhead, error) {
	player, err := s.player(ctx)
	if _, failed := errors.AsType[*device.PlaybackError](err); failed {
		return device.Playhead{}, device.ErrNoPosition
	}
	if err != nil {
		return device.Playhead{}, fmt.Errorf("reading the playback position: %w", err)
	}
	ms, unit := strings.CutSuffix(strings.TrimSpace(player.Position), " ms")
	at, err := strconv.ParseUint(ms, 10, 63)
	if !unit || err != nil || over(player.State) {
		return device.Playhead{}, device.ErrNoPosition
	}
	return device.Playhead{At: time.Duration(at) * time.Millisecond, Paused: player.State == statePause}, nil
}

// AwaitEnd polls the player until it closes or stops after having played.
func (s *session) AwaitEnd(ctx context.Context) error {
	var active bool
	started := time.Now()
	return device.AwaitPolledEnd(ctx, s.name, mediaPlayerQuery, func(ctx context.Context) (bool, error) {
		player, err := s.player(ctx)
		if err != nil {
			return false, err
		}
		state := player.State
		active = active || state == "play" || state == statePause
		if !active && time.Since(started) >= startupTimeout {
			return false, &device.PlaybackError{Err: fmt.Errorf("roku did not start playback within %s (state %q)", startupTimeout, state)}
		}
		return active && over(state), nil
	})
}

// Capabilities describes what Castor's channel plays.
func (s *session) Capabilities() *mediav1.Capabilities {
	caps := &mediav1.Capabilities{Provenance: "Castor Roku channel defaults; model/firmware limits unverified", SelfFetch: true}
	for _, audio := range []mediav1.AudioCodec{mediav1.AudioCodec_AUDIO_CODEC_AAC, mediav1.AudioCodec_AUDIO_CODEC_AC3, mediav1.AudioCodec_AUDIO_CODEC_EAC3} {
		tracks := &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: device.H264Support(), Audio: &mediav1.AudioSupport{Codec: audio, MaxChannels: 6}}}
		caps.Formats = append(caps.Formats, &mediav1.FormatSupport{Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}}, Tracks: tracks})
		for _, container := range []mediav1.ProgressiveSupport_Container{mediav1.ProgressiveSupport_CONTAINER_MP4, mediav1.ProgressiveSupport_CONTAINER_MKV} {
			caps.Formats = append(caps.Formats, &mediav1.FormatSupport{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: container}}, Tracks: tracks})
		}
	}
	return caps
}

func (s *session) Close() error { return nil }
