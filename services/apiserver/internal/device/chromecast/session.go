package chromecast

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"sync"

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
}

var _ device.Device = (*session)(nil)

// Play returns the receiver's own verdict on the LOAD, so a refused URL fails the hand-off; a dropped connection is gone.
func (s *session) Play(ctx context.Context, streamURL *url.URL, container mediav1.Container) error {
	if err := s.play(ctx, streamURL, device.MIME(container)); err != nil {
		select {
		case <-s.ch.gone:
			return &device.Gone{Device: s.name, Observed: "the Cast connection closed before the media loaded", Err: err}
		default:
			return err
		}
	}
	return nil
}

func (s *session) play(ctx context.Context, streamURL *url.URL, contentType string) error {
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
	s.watch.begin(streamURL.String())
	s.ending = mine
	s.watchMu.Unlock()
	reply, err := s.ch.request(ctx, transport, nsMedia, &castmedia.LoadMediaCommand{
		Type:     msgLoad,
		Media:    castmedia.MediaItem{ContentId: streamURL.String(), ContentType: contentType, StreamType: "BUFFERED"},
		Autoplay: true,
	})
	if err == nil {
		err = loadVerdict(reply)
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

// loadVerdict reads the answer to LOAD: a media status accepts it unless the player already failed.
func loadVerdict(reply []byte) error {
	var response castmedia.MediaStatusResponse
	if err := json.Unmarshal(reply, &response); err != nil {
		return fmt.Errorf("undecodable answer to LOAD: %w", err)
	}
	if response.Type != msgMediaStatus {
		return fmt.Errorf("chromecast refused the media (%s)", response.Type)
	}
	for _, status := range response.Status {
		if status.PlayerState == stateIdle && status.IdleReason == idleError {
			return errors.New("chromecast refused the media: the receiver went idle with an error")
		}
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
	return &mediav1.Capabilities{
		SelfFetch:       true,
		Containers:      []mediav1.Container{mediav1.Container_CONTAINER_HLS, mediav1.Container_CONTAINER_MPEGTS, mediav1.Container_CONTAINER_MP4, mediav1.Container_CONTAINER_WEBM},
		ServedContainer: mediav1.Container_CONTAINER_MP4,
		Video: []*mediav1.VideoSupport{
			device.VideoSupport(mediav1.Codec_CODEC_H264),
			device.VideoSupport(mediav1.Codec_CODEC_VP8),
		},
		Audio: []*mediav1.AudioSupport{
			{Codec: mediav1.Codec_CODEC_AAC, MaxChannels: 6},
			{Codec: mediav1.Codec_CODEC_AC3},
			{Codec: mediav1.Codec_CODEC_EAC3},
			{Codec: mediav1.Codec_CODEC_MP3},
			{Codec: mediav1.Codec_CODEC_VORBIS},
		},
	}
}
