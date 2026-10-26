package chromecast

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"sync"
	"time"

	castmedia "github.com/vishen/go-chromecast/cast"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

// session is one Cast connection to a device, following the Play it was last handed.
type session struct {
	ch   *channel
	name string

	watchMu sync.Mutex
	watch   playback
	ending  *ending
	// transport is the media receiver the last Play loaded on.
	transport string
}

var _ device.Device = (*session)(nil)

// Play returns the receiver's own verdict on the LOAD, so a refused URL fails the hand-off; a dropped connection is gone.
func (s *session) Play(ctx context.Context, playback device.Playback) error {
	if err := s.play(ctx, playback); err != nil {
		select {
		case <-s.ch.gone:
			return &device.Gone{Device: s.name, Observed: "the Cast connection closed before the media loaded", Err: err}
		default:
			return err
		}
	}
	return nil
}

func (s *session) play(ctx context.Context, playback device.Playback) error {
	stream, info := playback.URL.String(), playback.Info
	transport, err := s.mediaReceiver(ctx)
	if err != nil {
		return fmt.Errorf("starting the chromecast's media receiver: %w", err)
	}
	if err := s.ch.send(ctx, transport, nsConnection, &castmedia.PayloadHeader{Type: msgConnect}); err != nil {
		return fmt.Errorf("starting chromecast playback: %w", err)
	}
	// Each Play is awaited to its own end, so a retry after a refused one starts clean on the same connection.
	mine := newEnding()
	s.watchMu.Lock()
	s.watch.begin(stream)
	s.ending = mine
	s.transport = transport
	s.watchMu.Unlock()
	load := &loadCommand{
		Type: msgLoad, Autoplay: true,
		Media: mediaItem{MediaItem: castmedia.MediaItem{ContentId: stream, ContentType: device.MIME(playback.Container()), StreamType: "BUFFERED", Duration: float32(playback.Duration().Seconds())}},
	}
	load.Media.Metadata = castmedia.MediaMetadata{Title: info.GetTitle(), Subtitle: info.GetSynopsis(), ReleaseDate: info.GetReleaseDate()}
	if imageURL := info.GetImageUrl(); imageURL != "" {
		load.Media.Metadata.Images = []castmedia.Image{{URL: imageURL}}
	}
	load.Media.Tracks, load.ActiveTrackIds = textTracks(playback.Play.GetCaptions())
	reply, err := s.ch.request(ctx, transport, nsMedia, load)
	if err == nil {
		err = loadVerdict(reply, stream)
	}
	if err != nil {
		s.watchMu.Lock()
		// A Play begun since owns the watch now; this one's failure is not its to clear.
		if s.ending == mine {
			s.watch.disarm()
		}
		s.watchMu.Unlock()
		return fmt.Errorf("starting chromecast playback: %w", err)
	}
	return nil
}

// loadCommand is go-chromecast's LOAD with the text tracks it has no field for; Media shadows the embedded one.
type loadCommand struct {
	castmedia.LoadMediaCommand
	Media          mediaItem `json:"media"`
	ActiveTrackIds []int     `json:"activeTrackIds,omitempty"`
}

type mediaItem struct {
	castmedia.MediaItem
	Tracks []track `json:"tracks,omitempty"`
}

type track struct {
	TrackID          int    `json:"trackId"`
	Type             string `json:"type"`
	Subtype          string `json:"subtype"`
	TrackContentID   string `json:"trackContentId"`
	TrackContentType string `json:"trackContentType"`
	Language         string `json:"language,omitempty"`
	Name             string `json:"name,omitempty"`
}

// textTracks is captions as Cast text tracks, with the ids of those shown from the start.
func textTracks(captions []*mediav1.DeviceCommand_Caption) (tracks []track, active []int) {
	for i, c := range captions {
		tracks = append(tracks, track{
			TrackID: i + 1, Type: "TEXT", Subtype: "SUBTITLES", TrackContentID: c.GetUrl(),
			TrackContentType: device.CaptionMIME(c.GetFormat()), Language: c.GetLanguage(), Name: c.GetName(),
		})
		if c.GetDefault() {
			active = append(active, i+1)
		}
	}
	return tracks, active
}

func (s *session) Pause(ctx context.Context) error { return s.transportCommand(ctx, msgPause) }

func (s *session) Resume(ctx context.Context) error { return s.transportCommand(ctx, msgPlay) }

// Stop ends the media session the last Play opened, if the receiver reported one.
func (s *session) Stop(ctx context.Context) error { return s.transportCommand(ctx, msgStop) }

func (s *session) transportCommand(ctx context.Context, msg string) error {
	if _, _, err := s.mediaRequest(ctx, msg); err != nil {
		return fmt.Errorf("%s: %w", msg, err)
	}
	return nil
}

// Position asks, since a receiver pushes its status only when its state changes.
func (s *session) Position(ctx context.Context) (device.Playhead, error) {
	mediaSession, reply, err := s.mediaRequest(ctx, msgGetStatus)
	if errors.Is(err, errNoMediaSession) {
		return device.Playhead{}, device.ErrNoPosition
	}
	if err != nil {
		return device.Playhead{}, fmt.Errorf("reading the playback position: %w", err)
	}
	var response struct {
		Status []struct {
			MediaSessionID int      `json:"mediaSessionId"`
			CurrentTime    *float64 `json:"currentTime"`
			PlayerState    string   `json:"playerState"`
		} `json:"status"`
	}
	if json.Unmarshal(reply, &response) != nil {
		return device.Playhead{}, device.ErrNoPosition
	}
	for _, status := range response.Status {
		if status.MediaSessionID == mediaSession && status.CurrentTime != nil {
			return device.Playhead{At: time.Duration(*status.CurrentTime * float64(time.Second)), Paused: status.PlayerState == "PAUSED"}, nil
		}
	}
	return device.Playhead{}, device.ErrNoPosition
}

var errNoMediaSession = errors.New("the chromecast has reported no media session yet")

// mediaRequest sends msg to the media session the last Play opened, returning that session and the receiver's MEDIA_STATUS.
func (s *session) mediaRequest(ctx context.Context, msg string) (int, []byte, error) {
	s.watchMu.Lock()
	transport, mediaSession := s.transport, s.watch.mediaSession
	s.watchMu.Unlock()
	if mediaSession == 0 {
		return 0, nil, errNoMediaSession
	}
	reply, err := s.ch.request(ctx, transport, nsMedia, &castmedia.MediaHeader{Type: msg, MediaSessionId: mediaSession})
	if err != nil {
		return 0, nil, err
	}
	var header castmedia.PayloadHeader
	if err := json.Unmarshal(reply, &header); err != nil || header.Type != msgMediaStatus {
		return 0, nil, fmt.Errorf("the chromecast answered %s", header.Type)
	}
	return mediaSession, reply, nil
}

// loadVerdict accepts the requested stream only when the receiver's answer does not identify a different one.
func loadVerdict(reply []byte, contentID string) error {
	var response castmedia.MediaStatusResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		return fmt.Errorf("undecodable answer to LOAD: %w", err)
	}
	if response.Type != msgMediaStatus {
		return fmt.Errorf("chromecast refused the media (%s)", response.Type)
	}
	if len(response.Status) == 0 {
		return errors.New("chromecast answered LOAD without a media status")
	}
	var matched bool
	var other string
	for _, status := range response.Status {
		got := status.Media.ContentId
		if got != "" && got != contentID {
			other = got
			continue
		}
		matched = matched || got == contentID
		if status.PlayerState == stateIdle && status.IdleReason == idleError {
			return errors.New("chromecast refused the media: the receiver went idle with an error")
		}
	}
	if !matched && other != "" {
		return fmt.Errorf("chromecast answered LOAD with a different stream %q", other)
	}
	return nil
}

// mediaReceiver returns the Default Media Receiver's transport, launching it unless it already runs.
func (s *session) mediaReceiver(ctx context.Context) (string, error) {
	status, err := s.receiverStatus(ctx, &castmedia.PayloadHeader{Type: msgGetStatus})
	if err != nil {
		return "", err
	}
	if transport, ok := defaultMediaTransport(status); ok {
		return transport, nil
	}
	status, err = s.receiverStatus(ctx, &castmedia.LaunchRequest{Type: "LAUNCH", AppId: defaultMediaReceiver})
	if err != nil {
		return "", err
	}
	if transport, ok := defaultMediaTransport(status); ok {
		return transport, nil
	}
	return "", errors.New("the chromecast answered the launch without the Default Media Receiver running")
}

func (s *session) receiverStatus(ctx context.Context, payload castmedia.Payload) (castmedia.ReceiverStatusResponse, error) {
	var status castmedia.ReceiverStatusResponse
	reply, err := s.ch.request(ctx, receiverID, nsReceiver, payload)
	if err != nil {
		return status, err
	}
	if err := json.Unmarshal(reply, &status); err != nil {
		return status, fmt.Errorf("undecodable receiver status: %w", err)
	}
	if status.Type != "RECEIVER_STATUS" {
		return status, fmt.Errorf("the chromecast answered %s", status.Type)
	}
	return status, nil
}

func defaultMediaTransport(status castmedia.ReceiverStatusResponse) (string, bool) {
	for _, app := range status.Status.Applications {
		if app.AppId == defaultMediaReceiver && app.TransportId != "" {
			return app.TransportId, true
		}
	}
	return "", false
}

func (s *session) Close() error {
	return s.ch.Close()
}

func (s *session) Capabilities() *mediav1.Capabilities {
	caps := &mediav1.Capabilities{Provenance: "Cast default Web Receiver family envelopes; model/firmware limits unverified", SelfFetch: true}
	for _, container := range []mediav1.ProgressiveSupport_Container{mediav1.ProgressiveSupport_CONTAINER_MP4, mediav1.ProgressiveSupport_CONTAINER_MPEGTS} {
		for _, audio := range []mediav1.AudioCodec{mediav1.AudioCodec_AUDIO_CODEC_AAC, mediav1.AudioCodec_AUDIO_CODEC_AC3, mediav1.AudioCodec_AUDIO_CODEC_EAC3, mediav1.AudioCodec_AUDIO_CODEC_MP3} {
			caps.Formats = append(caps.Formats, &mediav1.FormatSupport{
				Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: container}},
				Tracks:    &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: device.H264Support(), Audio: &mediav1.AudioSupport{Codec: audio, MaxChannels: 6}}},
				Captions:  []mediav1.CaptionFormat{mediav1.CaptionFormat_CAPTION_FORMAT_VTT},
			})
		}
	}
	// https://developers.google.com/cast/docs/media/streaming_protocols#http_live_streaming_hls
	for _, audio := range []mediav1.AudioCodec{mediav1.AudioCodec_AUDIO_CODEC_AAC, mediav1.AudioCodec_AUDIO_CODEC_AC3, mediav1.AudioCodec_AUDIO_CODEC_EAC3, mediav1.AudioCodec_AUDIO_CODEC_MP3} {
		caps.Formats = append(caps.Formats, &mediav1.FormatSupport{
			Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{Subtitles: mediav1.HLSSupport_SUBTITLES_WEBVTT}},
			Tracks:    &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: device.H264Support(), Audio: &mediav1.AudioSupport{Codec: audio, MaxChannels: 6}}},
			Captions:  []mediav1.CaptionFormat{mediav1.CaptionFormat_CAPTION_FORMAT_VTT},
		})
	}
	caps.Formats = append(caps.Formats, &mediav1.FormatSupport{
		Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_WEBM}},
		Tracks:    &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: &mediav1.VideoSupport{Codec: &mediav1.VideoSupport_Vp8{Vp8: &mediav1.VP8Support{}}}, Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_VORBIS}}},
		Captions:  []mediav1.CaptionFormat{mediav1.CaptionFormat_CAPTION_FORMAT_VTT},
	})
	return caps
}
