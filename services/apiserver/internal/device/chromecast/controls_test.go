package chromecast

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/devicetest"
	castmedia "github.com/vishen/go-chromecast/cast"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestFiniteHLSWaitsForPlaybackAndRecoversAReceiverStuckAtZero(t *testing.T) {
	for _, mode := range []string{"starts normally", "stuck until seek", "never starts", "still buffering after seek"} {
		t.Run(mode, func(t *testing.T) {
			near, far := net.Pipe()
			dev := &session{ending: newEnding()}
			dev.ch = newChannel(near, dev.watchMessage)
			var started atomic.Bool
			var nudges atomic.Int32
			var receiver *channel
			var content string
			receiver = newChannel(far, func(payload []byte) {
				var req struct {
					Type      string              `json:"type"`
					RequestID int                 `json:"requestId"`
					Session   int                 `json:"mediaSessionId"`
					Media     castmedia.MediaItem `json:"media"`
					At        float64             `json:"currentTime"`
					Resume    string              `json:"resumeState"`
				}
				if json.Unmarshal(payload, &req) != nil {
					return
				}
				if req.Type == msgGetStatus && content == "" {
					_ = receiver.send(t.Context(), senderID, nsReceiver, running(req.RequestID))
					return
				}
				state, at := "BUFFERING", 0.0
				switch req.Type {
				case msgLoad:
					content = req.Media.ContentId
				case "SEEK":
					nudges.Add(1)
					if req.Session != 1 || req.Resume != "PLAYBACK_START" || req.At <= 0 || req.At > 1 {
						t.Errorf("invalid startup seek: session=%d resume=%q at=%v", req.Session, req.Resume, req.At)
					}
					if mode == "stuck until seek" {
						state, at = "PLAYING", req.At
					}
				case msgGetStatus:
					if mode == "starts normally" {
						state, at = "PLAYING", 0.5
					}
				default:
					return
				}
				if state == "PLAYING" {
					started.Store(true)
				}
				reply := fmt.Sprintf(`{"type":"MEDIA_STATUS","requestId":%d,"status":[{"mediaSessionId":1,"media":{"contentId":%q},"playerState":%q,"currentTime":%v,"supportedMediaCommands":3,"liveSeekableRange":{"start":0,"end":30}}]}`, req.RequestID, content, state, at)
				_ = receiver.send(t.Context(), senderID, nsMedia, jsontext.Value(reply))
			})
			t.Cleanup(func() { _ = dev.ch.Close(); _ = receiver.Close() })
			play := devicetest.Playlist(mediav1.Container_CONTAINER_MP4)
			play.GetPlaylist().Duration = durationpb.New(time.Hour)
			u, _ := url.Parse("http://media.test/master.m3u8")
			timeout := 6 * time.Second
			if mode == "never starts" {
				timeout = 350 * time.Millisecond
			}
			if mode == "still buffering after seek" {
				timeout = 4 * time.Second
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			err := dev.Play(ctx, device.Playback{URL: u, Play: play})
			if mode == "never starts" || mode == "still buffering after seek" {
				if err == nil {
					t.Fatal("LOAD acknowledgement reported success while playback stayed buffering")
				}
				want := int32(0)
				if mode == "still buffering after seek" {
					want = 1
				}
				if nudges.Load() != want {
					t.Fatalf("startup seeks=%d, want %d", nudges.Load(), want)
				}
				return
			}
			if err != nil || !started.Load() {
				t.Fatalf("playback did not start: started=%v err=%v", started.Load(), err)
			}
			want := int32(0)
			if mode == "stuck until seek" {
				want = 1
			}
			if nudges.Load() != want {
				t.Fatalf("startup seeks=%d, want %d", nudges.Load(), want)
			}
		})
	}
}

func TestHLSWaitsThroughLoadingIdleAndTheAssignmentOfItsMediaSession(t *testing.T) {
	for _, sessionID := range []int{0, 1} {
		for _, extended := range []bool{false, true} {
			t.Run(fmt.Sprintf("initial_session=%d/extended_loading=%v", sessionID, extended), func(t *testing.T) {
				loaded := false
				content := "http://media.test/master.m3u8"
				activeSession := sessionID
				if activeSession == 0 {
					activeSession = 7
				}
				dev := receiverSaying(t, func(id int) any {
					if !loaded {
						return running(id)
					}
					reply := mediaStatus(id, content, "PLAYING", "")
					reply.Status[0].MediaSessionId = activeSession
					reply.Status[0].CurrentTime = 0.5
					return reply
				}, func(id int, media mediaItem) []any {
					loaded = true
					extension := ""
					if extended {
						extension = `,"extendedStatus":{"playerState":"LOADING"}`
					}
					return []any{jsontext.Value(fmt.Sprintf(`{"type":"MEDIA_STATUS","requestId":%d,"status":[{"mediaSessionId":%d,"playerState":"IDLE","media":{"contentId":%q}%s}]}`, id, sessionID, media.ContentId, extension))}
				})
				play := devicetest.Playlist(mediav1.Container_CONTAINER_MP4)
				play.GetPlaylist().Duration = durationpb.New(time.Hour)
				u, _ := url.Parse(content)
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				if err := dev.Play(ctx, device.Playback{URL: u, Play: play}); err != nil {
					t.Fatalf("initial loading was refused: %v", err)
				}
				head, err := dev.Position(ctx)
				if err != nil || head.At != 500*time.Millisecond {
					t.Fatalf("new media session not followed: %+v, %v", head, err)
				}
			})
		}
	}
}

func TestHLSTerminalIdleReasonsStillRefusePlayback(t *testing.T) {
	for _, reason := range []string{"ERROR", "CANCELLED", "INTERRUPTED", "FINISHED"} {
		t.Run(reason, func(t *testing.T) {
			dev := receiverSaying(t, func(id int) any { return running(id) }, func(id int, media mediaItem) []any {
				return []any{mediaStatus(id, media.ContentId, stateIdle, reason)}
			})
			play := devicetest.Playlist(mediav1.Container_CONTAINER_MP4)
			play.GetPlaylist().Duration = durationpb.New(time.Hour)
			u, _ := url.Parse("http://media.test/master.m3u8")
			ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer cancel()
			err := dev.Play(ctx, device.Playback{URL: u, Play: play})
			if err == nil || reason != "ERROR" && !strings.Contains(err.Error(), reason) {
				t.Fatalf("terminal idle %s was not preserved: %v", reason, err)
			}
		})
	}
}

func TestChromecastReportsTheActualHLSSeekableWindow(t *testing.T) {
	state, err := playhead([]byte(`{"status":[{"mediaSessionId":1,"currentTime":71.89,"playerState":"PLAYING","liveSeekableRange":{"start":0,"end":72.5,"isMovingWindow":true}}]}`), 1)
	if err != nil || state.Seekable == nil || state.Seekable.Start != 0 || state.Seekable.End != 72500*time.Millisecond {
		t.Fatalf("seek window=%+v, %v", state, err)
	}
}

func TestServedHLSDeclaresFMP4WithoutOverridingSourcePlaylists(t *testing.T) {
	for _, served := range []bool{false, true} {
		t.Run(fmt.Sprint("served=", served), func(t *testing.T) {
			near, far := net.Pipe()
			dev := &session{ending: newEnding()}
			dev.ch = newChannel(near, dev.watchMessage)
			loaded := make(chan mediaItem, 1)
			var receiver *channel
			receiver = newChannel(far, func(payload []byte) {
				var req struct {
					Type      string    `json:"type"`
					RequestID int       `json:"requestId"`
					Media     mediaItem `json:"media"`
				}
				if json.Unmarshal(payload, &req) != nil {
					return
				}
				switch req.Type {
				case msgGetStatus:
					_ = receiver.send(t.Context(), senderID, nsReceiver, running(req.RequestID))
				case msgLoad:
					loaded <- req.Media
					_ = receiver.send(t.Context(), senderID, nsMedia, mediaStatus(req.RequestID, req.Media.ContentId, "PLAYING", ""))
				}
			})
			t.Cleanup(func() { _ = dev.Close(); _ = receiver.Close() })
			play := devicetest.Source(mediav1.Container_CONTAINER_HLS)
			if served {
				play = devicetest.Playlist(mediav1.Container_CONTAINER_MP4)
			}
			u, _ := url.Parse("http://media.test/master.m3u8")
			if err := dev.Play(t.Context(), device.Playback{URL: u, Play: play}); err != nil {
				t.Fatal(err)
			}
			got := <-loaded
			want := ""
			if served {
				want = "fmp4"
			}
			if got.HLSSegmentFormat != want || got.HLSVideoSegmentFormat != want {
				t.Fatalf("HLS formats=%q/%q, want %q", got.HLSSegmentFormat, got.HLSVideoSegmentFormat, want)
			}
		})
	}
}

func TestChromecastSeekUsesTheCurrentSessionAndPreservesPause(t *testing.T) {
	for _, resets := range []bool{false, true} {
		t.Run(fmt.Sprint("receiver_restarts=", resets), func(t *testing.T) {
			near, far := net.Pipe()
			dev := &session{ending: newEnding()}
			dev.ch = newChannel(near, dev.watchMessage)
			var receiver *channel
			var content string
			state, position := "PLAYING", float32(0)
			type sought struct {
				at      float64
				session int
				resume  string
			}
			seeks := make(chan sought, 1)
			receiver = newChannel(far, func(payload []byte) {
				var req struct {
					Type        string              `json:"type"`
					RequestID   int                 `json:"requestId"`
					Session     int                 `json:"mediaSessionId"`
					Media       castmedia.MediaItem `json:"media"`
					CurrentTime float64             `json:"currentTime"`
					Resume      string              `json:"resumeState"`
				}
				if json.Unmarshal(payload, &req) != nil {
					return
				}
				if req.Type == "GET_STATUS" && req.Session == 0 {
					_ = receiver.send(t.Context(), senderID, nsMedia, running(req.RequestID))
					return
				}
				switch req.Type {
				case "LOAD":
					content = req.Media.ContentId
				case "PAUSE":
					state = "PAUSED"
				case "PLAY":
					state = "PLAYING"
				case "SEEK":
					position = float32(req.CurrentTime)
					if resets {
						position = 0
					}
					seeks <- sought{req.CurrentTime, req.Session, req.Resume}
				}
				media := mediaStatus(req.RequestID, content, state, "").Status[0]
				media.CurrentTime = position
				reply := struct {
					Type      string `json:"type"`
					RequestID int    `json:"requestId"`
					Status    []struct {
						castmedia.Media
						Commands uint64 `json:"supportedMediaCommands"`
					} `json:"status"`
				}{Type: msgMediaStatus, RequestID: req.RequestID}
				reply.Status = append(reply.Status, struct {
					castmedia.Media
					Commands uint64 `json:"supportedMediaCommands"`
				}{media, 3})
				_ = receiver.send(t.Context(), senderID, nsMedia, reply)
			})
			t.Cleanup(func() { _ = dev.Close(); _ = receiver.Close() })
			u, _ := url.Parse("http://media.test/movie.mp4")
			if err := dev.Play(t.Context(), device.Playback{URL: u, Play: devicetest.Source(mediav1.Container_CONTAINER_MP4)}); err != nil {
				t.Fatal(err)
			}
			if controls := dev.Controls(); !controls.Pause || !controls.Seek {
				t.Fatalf("receiver controls not observed: %+v", controls)
			}
			if err := dev.Pause(t.Context()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 350*time.Millisecond)
			defer cancel()
			err := dev.Seek(ctx, 40*time.Second)
			if resets {
				if err == nil {
					t.Fatal("acknowledged a successful seek while the receiver restarted at zero")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := <-seeks
			if got.at != 40 || got.session != 1 || got.resume != "" {
				t.Fatalf("SEEK=%+v; want current session, 40 seconds and unchanged pause", got)
			}
			head, err := dev.Position(t.Context())
			if err != nil || head.At != 40*time.Second || !head.Paused {
				t.Fatalf("paused playhead=%+v, %v", head, err)
			}
		})
	}
}
