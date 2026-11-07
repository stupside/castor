package fcast

import (
	"context"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"buf.build/go/protovalidate"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/devicetest"
)

// packet is one FCast packet as the fake receiver reads or writes it.
type packet struct {
	op   opcode
	body string
}

// fakeReceiver is an FCast receiver at the far end of a pipe; it frames packets by the spec, not with castor's channel.
type fakeReceiver struct {
	conn    net.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	got     []packet
	flushes chan chan struct{}
}

// connectReceiver opens castor's session to a fake receiver announcing version, none when 0;
// answer replies to every packet castor sends but Ping and Pong, which the receiver handles itself.
func connectReceiver(t *testing.T, version int, answer func(r *fakeReceiver, p packet)) (*session, *fakeReceiver, error) {
	t.Helper()
	near, far := net.Pipe()
	r := &fakeReceiver{conn: far, flushes: make(chan chan struct{}, 1)}
	incoming := make(chan packet, 256)
	go r.read(incoming)
	go r.serve(version, incoming, answer)
	t.Cleanup(func() { _ = far.Close() })
	dev, err := open(t.Context(), near, "Living Room")
	if err == nil {
		t.Cleanup(func() { _ = dev.Close() })
	}
	return dev, r, err
}

// serve answers castor from one goroutine, so a flush's ping always follows the answers sent before it.
func (r *fakeReceiver) serve(version int, incoming <-chan packet, answer func(*fakeReceiver, packet)) {
	if version > 0 {
		r.send(opVersion, fmt.Sprintf(`{"version":%d}`, version))
	}
	var flushing []chan struct{}
	for {
		select {
		case p, ok := <-incoming:
			if !ok {
				return
			}
			r.mu.Lock()
			r.got = append(r.got, p)
			r.mu.Unlock()
			switch p.op {
			case opPing:
				r.send(opPong, "")
			case opPong:
				if len(flushing) > 0 {
					close(flushing[0])
					flushing = flushing[1:]
				}
			default:
				if answer != nil {
					answer(r, p)
				}
			}
		case done := <-r.flushes:
			flushing = append(flushing, done)
			r.send(opPing, "")
		}
	}
}

func (r *fakeReceiver) read(incoming chan<- packet) {
	defer close(incoming)
	for {
		var header [5]byte
		if _, err := io.ReadFull(r.conn, header[:]); err != nil {
			return
		}
		body := make([]byte, binary.LittleEndian.Uint32(header[:4])-1)
		if _, err := io.ReadFull(r.conn, body); err != nil {
			return
		}
		incoming <- packet{op: opcode(header[4]), body: string(body)}
	}
}

func (r *fakeReceiver) send(op opcode, body string) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	frame := append(binary.LittleEndian.AppendUint32(nil, uint32(1+len(body))), byte(op))
	_, _ = r.conn.Write(append(frame, body...))
}

func (r *fakeReceiver) say(packets ...packet) {
	for _, p := range packets {
		r.send(p.op, p.body)
	}
}

// flush returns once castor has handled everything the receiver sent before it, castor answering its ping only after them.
func (r *fakeReceiver) flush(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	r.flushes <- done
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("castor never answered the receiver's ping")
	}
}

// sent is what castor sent, but its pings and pongs, as of the last flush.
func (r *fakeReceiver) sent() []packet {
	r.mu.Lock()
	defer r.mu.Unlock()
	var got []packet
	for _, p := range r.got {
		if p.op != opPing && p.op != opPong {
			got = append(got, p)
		}
	}
	return got
}

// greets is a v3 receiver: it greets castor once castor states its version, and hands each Play's URL to onPlay.
func greets(onPlay func(r *fakeReceiver, url string)) func(*fakeReceiver, packet) {
	return func(r *fakeReceiver, p packet) {
		switch p.op {
		case opVersion:
			r.send(opInitial, `{"displayName":"Living Room","appName":"FCast","appVersion":"2.0.0"}`)
		case opPlay:
			var play struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal([]byte(p.body), &play)
			if onPlay != nil {
				onPlay(r, play.URL)
			}
		}
	}
}

// echoes takes every Play, echoing it to its senders before sending then.
func echoes(then ...packet) func(r *fakeReceiver, url string) {
	return func(r *fakeReceiver, url string) {
		r.send(opPlayUpdate, playUpdate(url))
		r.say(then...)
	}
}

func playUpdate(url string) string {
	return fmt.Sprintf(`{"generationTime":1700000000000,"playData":{"container":"video/mp4","url":%q}}`, url)
}

func update(state int, at float64) packet {
	return packet{opPlaybackUpdate, fmt.Sprintf(`{"generationTime":1700000000000,"state":%d,"time":%g,"duration":600,"speed":1}`, state, at)}
}

func itemEnded(url string) packet {
	return packet{opEvent, fmt.Sprintf(`{"generationTime":1700000000000,"event":{"type":1,"item":{"container":"video/mp4","url":%q}}}`, url)}
}

func failure(message string) packet {
	return packet{opPlaybackError, fmt.Sprintf(`{"message":%q}`, message)}
}

const stream = "http://media.test/movie.mp4"

func mp4(t *testing.T) device.Playback {
	t.Helper()
	u, err := url.Parse(stream)
	if err != nil {
		t.Fatal(err)
	}
	return device.Playback{URL: u, Play: devicetest.Source(mediav1.Container_CONTAINER_MP4)}
}

func TestTheHandshakeStatesCastorsVersionFirstThenSubscribesToItemEnds(t *testing.T) {
	_, r, err := connectReceiver(t, 3, greets(nil))
	if err != nil {
		t.Fatal(err)
	}
	r.flush(t)
	got := r.sent()
	if len(got) != 3 || got[0] != (packet{opVersion, `{"version":3}`}) || got[1].op != opInitial || got[2] != (packet{opSubscribeEvent, `{"event":{"type":1}}`}) {
		t.Errorf("handshake = %+v, want Version 3, Initial, then a subscription to MediaItemEnd", got)
	}
}

func TestOnlyAReceiverSpeakingV3OrLaterIsConnected(t *testing.T) {
	for version, connects := range map[int]bool{1: false, 2: false, 3: true, 4: true} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			_, r, err := connectReceiver(t, version, greets(nil))
			if (err == nil) != connects {
				t.Fatalf("connecting to a v%d receiver = %v, want connected %v", version, err, connects)
			}
			if !connects {
				return
			}
			r.flush(t)
			if got := r.sent()[0]; got.body != `{"version":3}` {
				t.Errorf("castor told a v%d receiver %s, want v3, the only version it speaks", version, got.body)
			}
		})
	}
}

func TestAReceiverThatNeverStatesItsVersionIsNotConnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		if _, _, err := connectReceiver(t, 0, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("connecting = %v, want a deadline exceeded waiting for the receiver's version", err)
		}
	})
}

func TestAPlayCarriesTheMediaAndItsDetailsButNothingTheReceiverWouldApply(t *testing.T) {
	for _, tt := range []struct {
		name string
		info *castorv1.MediaInfo
		want map[string]any
	}{
		{"a title and its image", &castorv1.MediaInfo{Title: "The Matrix", ImageUrl: "https://image.test/matrix.jpg"}, map[string]any{
			"container": "application/x-mpegURL", "url": stream,
			"metadata": map[string]any{"type": float64(0), "title": "The Matrix", "thumbnailUrl": "https://image.test/matrix.jpg"},
		}},
		{"nothing to show", nil, map[string]any{"container": "application/x-mpegURL", "url": stream}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dev, r, err := connectReceiver(t, 3, greets(echoes()))
			if err != nil {
				t.Fatal(err)
			}
			playback := mp4(t)
			playback.Play, playback.Info = devicetest.Source(mediav1.Container_CONTAINER_HLS), tt.info
			if err := dev.Play(t.Context(), playback); err != nil {
				t.Fatal(err)
			}
			r.flush(t)
			var got map[string]any
			if err := json.Unmarshal([]byte(r.sent()[3].body), &got); err != nil {
				t.Fatal(err)
			}
			// A volume, speed or time present at zero would mute, freeze or rewind the receiver.
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Play = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlayReturnsTheReceiversAnswerToIt(t *testing.T) {
	for _, tt := range []struct {
		name    string
		onPlay  func(r *fakeReceiver, url string)
		wantErr string
	}{
		{"its echo takes it", echoes(), ""},
		{"an error before its echo refuses it", func(r *fakeReceiver, _ string) {
			r.say(failure("unsupported scheme"))
		}, "unsupported scheme"},
		{"the failure of what played before does not refuse it", func(r *fakeReceiver, url string) {
			r.say(update(stateIdle, 0), failure("the previous stream broke"))
			echoes(update(statePlaying, 1))(r, url)
		}, ""},
		{"an echo of other media does not take it", func(r *fakeReceiver, _ string) {
			r.send(opPlayUpdate, playUpdate("http://other.test/clip.mp4"))
		}, "did not take the media"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dev, _, err := connectReceiver(t, 3, greets(tt.onPlay))
				if err != nil {
					t.Fatal(err)
				}
				err = dev.Play(t.Context(), mp4(t))
				if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
					t.Errorf("Play = %v, want error %q", err, tt.wantErr)
				}
			})
		})
	}
}

func TestAPlayTooLongForTheProtocolIsNotSent(t *testing.T) {
	dev, r, err := connectReceiver(t, 3, greets(echoes()))
	if err != nil {
		t.Fatal(err)
	}
	playback := mp4(t)
	playback.Info = &castorv1.MediaInfo{Title: strings.Repeat("x", maxPacket)}
	if err := dev.Play(t.Context(), playback); err == nil {
		t.Fatal("a Play longer than the protocol's packet was accepted")
	}
	r.flush(t)
	for _, p := range r.sent() {
		if p.op == opPlay {
			t.Fatalf("castor sent %d bytes of a Play the protocol cannot carry", len(p.body))
		}
	}
}

func TestAPlayOfAContainerFCastCannotNameIsNotSent(t *testing.T) {
	dev, r, err := connectReceiver(t, 3, greets(echoes()))
	if err != nil {
		t.Fatal(err)
	}
	playback := mp4(t)
	playback.Play = devicetest.Source(mediav1.Container(99))
	if err := dev.Play(t.Context(), playback); err == nil {
		t.Fatal("a Play with no container type was accepted")
	}
	r.flush(t)
	for _, p := range r.sent() {
		if p.op == opPlay {
			t.Fatalf("castor sent a Play naming no container: %s", p.body)
		}
	}
}

func TestAPlayOnADroppedConnectionIsGone(t *testing.T) {
	dev, r, err := connectReceiver(t, 3, greets(echoes()))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.conn.Close()
	<-dev.ch.gone
	if _, gone := errors.AsType[*device.Gone](dev.Play(t.Context(), mp4(t))); !gone {
		t.Error("a Play on a connection the receiver dropped is not reported gone, so its lender never connects again")
	}
}

func TestAPlayAbandonedForANewerOneLeavesTheNewerOneWatched(t *testing.T) {
	const abandoned = "http://media.test/attempt-1.mp4"
	loading := make(chan struct{})
	dev, r, err := connectReceiver(t, 3, greets(func(r *fakeReceiver, url string) {
		if url == abandoned {
			close(loading)
			return
		}
		echoes(update(statePlaying, 4))(r, url)
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, abandon := context.WithCancel(t.Context())
	first := make(chan error, 1)
	go func() {
		playback := mp4(t)
		playback.URL = &url.URL{Scheme: "http", Host: "media.test", Path: "/attempt-1.mp4"}
		first <- dev.Play(ctx, playback)
	}()
	<-loading
	if err := dev.Play(t.Context(), mp4(t)); err != nil {
		t.Fatal(err)
	}
	abandon()
	if err := <-first; err == nil {
		t.Fatal("the abandoned Play reported the media it never heard echoed as taken")
	}
	r.flush(t)
	if got, err := dev.Position(t.Context()); err != nil || got.At != 4*time.Second {
		t.Errorf("after the abandoned Play failed Position = %v, %v; want the newer Play's 4s", got, err)
	}
}

func TestUpdatesBeforeTheEchoAreThoseOfWhatPlayedBefore(t *testing.T) {
	dev, r, err := connectReceiver(t, 3, func(r *fakeReceiver, p packet) {
		switch p.op {
		case opVersion:
			// Receivers greet a sender with the state they hold, of media played earlier.
			r.send(opInitial, `{"appName":"FCast"}`)
			r.say(update(statePlaying, 999))
		case opPlay:
			r.say(update(statePlaying, 500))
			r.send(opPlayUpdate, playUpdate(stream))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.Play(t.Context(), mp4(t)); err != nil {
		t.Fatal(err)
	}
	r.flush(t)
	if got, err := dev.Position(t.Context()); !errors.Is(err, device.ErrNoPosition) {
		t.Errorf("Position = %v, %v before any update of the handed media, want ErrNoPosition", got, err)
	}
	r.say(update(statePlaying, 12.5))
	r.flush(t)
	if got, err := dev.Position(t.Context()); err != nil || got.At != 12500*time.Millisecond {
		t.Errorf("Position = %v, %v; want the handed media's 12.5s", got, err)
	}
}

func TestPositionIsTheLastOneReportedForTheHandedMedia(t *testing.T) {
	for _, tt := range []struct {
		name    string
		updates []packet
		want    device.Playhead
		err     error
	}{
		{"playing", []packet{update(statePlaying, 12.5)}, device.Playhead{At: 12500 * time.Millisecond}, nil},
		{"paused by its viewer", []packet{update(statePlaying, 12.5), update(statePaused, 30)}, device.Playhead{At: 30 * time.Second, Paused: true}, nil},
		{"idle since it played", []packet{update(statePlaying, 12.5), update(stateIdle, 0)}, device.Playhead{}, device.ErrNoPosition},
		{"no time", []packet{{opPlaybackUpdate, `{"state":1}`}}, device.Playhead{}, device.ErrNoPosition},
		{"a negative time", []packet{update(statePlaying, -3)}, device.Playhead{}, device.ErrNoPosition},
		{"a time no cast reaches", []packet{update(statePlaying, 1e300)}, device.Playhead{}, device.ErrNoPosition},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dev, r, err := connectReceiver(t, 3, greets(echoes(tt.updates...)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dev.Position(t.Context()); !errors.Is(err, device.ErrNoPosition) {
				t.Fatalf("Position before any Play = %v, want ErrNoPosition", err)
			}
			if err := dev.Play(t.Context(), mp4(t)); err != nil {
				t.Fatal(err)
			}
			r.flush(t)
			if got, err := dev.Position(t.Context()); got != tt.want || !errors.Is(err, tt.err) {
				t.Errorf("Position = %v, %v; want %v, %v", got, err, tt.want, tt.err)
			}
		})
	}
}

func TestPauseResumeAndStopSendTheirCommands(t *testing.T) {
	dev, r, err := connectReceiver(t, 3, greets(echoes()))
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []func(context.Context) error{dev.Pause, dev.Resume, dev.Stop} {
		if err := command(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	r.flush(t)
	got := r.sent()[3:]
	if want := []packet{{opPause, ""}, {opResume, ""}, {opStop, ""}}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %+v, want %+v", got, want)
	}
}

func TestAwaitEndFollowsTheHandedMediaToItsEnd(t *testing.T) {
	type outcome string
	const (
		running outcome = "still running"
		ended   outcome = "ended"
		failed  outcome = "failed"
	)
	for _, tt := range []struct {
		name  string
		after []packet
		want  outcome
	}{
		{"its item playing to the end ends it", []packet{update(statePlaying, 1), update(statePaused, 600), itemEnded(stream)}, ended},
		{"another item's end does not", []packet{update(statePlaying, 1), itemEnded("http://other.test/clip.mp4")}, running},
		{"a paused receiver is still casting", []packet{update(statePlaying, 1), update(statePaused, 2)}, running},
		{"a stop after playing ends it", []packet{update(statePlaying, 1), update(stateIdle, 0)}, ended},
		{"an error right behind the stop fails it", []packet{update(statePlaying, 1), update(stateIdle, 0), failure("decoder error")}, failed},
		{"an error while playing fails it", []packet{update(statePlaying, 1), failure("network error")}, failed},
		{"an error before playing fails it", []packet{failure("unsupported codec")}, failed},
		{"the idle a player starts in does not end it", []packet{update(stateIdle, 0)}, running},
		{"another sender's media ends it", []packet{update(statePlaying, 1), {opPlayUpdate, playUpdate("http://other.test/clip.mp4")}}, ended},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dev, _, err := connectReceiver(t, 3, greets(echoes(tt.after...)))
				if err != nil {
					t.Fatal(err)
				}
				if err := dev.Play(t.Context(), mp4(t)); err != nil {
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() { result <- dev.AwaitEnd(t.Context()) }()
				time.Sleep(idleSettle + time.Second)
				got := running
				select {
				case err := <-result:
					got = ended
					if _, refused := errors.AsType[*device.PlaybackError](err); refused {
						got = failed
					} else if err != nil {
						t.Fatalf("AwaitEnd = %v", err)
					}
				default:
				}
				if got != tt.want {
					t.Errorf("the cast %s, want it %s", got, tt.want)
				}
			})
		})
	}
}

func TestAnItemEndBeforeTheEchoIsWhatPlayedBefore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// The same URL handed again: the end of its last play may still be on its way.
		dev, _, err := connectReceiver(t, 3, greets(func(r *fakeReceiver, url string) {
			r.say(itemEnded(url))
			echoes(update(statePlaying, 1))(r, url)
		}))
		if err != nil {
			t.Fatal(err)
		}
		if err := dev.Play(t.Context(), mp4(t)); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- dev.AwaitEnd(t.Context()) }()
		time.Sleep(idleSettle + time.Second)
		select {
		case err := <-result:
			t.Errorf("AwaitEnd = %v, ended by an end the receiver sent before it took this Play", err)
		default:
		}
	})
}

func TestAReceiverThatNeverStartsFailsTheCast(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dev, _, err := connectReceiver(t, 3, greets(echoes()))
		if err != nil {
			t.Fatal(err)
		}
		if err := dev.Play(t.Context(), mp4(t)); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- dev.AwaitEnd(t.Context()) }()
		time.Sleep(startupTimeout - time.Second)
		select {
		case err := <-result:
			t.Fatalf("AwaitEnd = %v before the receiver had its time to start", err)
		default:
		}
		time.Sleep(2 * time.Second)
		select {
		case err := <-result:
			if _, refused := errors.AsType[*device.PlaybackError](err); !refused {
				t.Errorf("AwaitEnd = %v, want a playback error", err)
			}
		default:
			t.Fatal("a receiver that never started still holds the cast")
		}
	})
}

func TestAReceiverThatDropsTheConnectionIsGone(t *testing.T) {
	dev, r, err := connectReceiver(t, 3, greets(echoes(update(statePlaying, 1))))
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.Play(t.Context(), mp4(t)); err != nil {
		t.Fatal(err)
	}
	_ = r.conn.Close()
	if _, gone := errors.AsType[*device.Gone](dev.AwaitEnd(t.Context())); !gone {
		t.Error("a receiver that went away mid-cast is not reported gone, so the cast outlives it")
	}
}

func TestAwaitingWhatWasNeverPlayedFails(t *testing.T) {
	dev, _, err := connectReceiver(t, 3, greets(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.AwaitEnd(t.Context()); err == nil {
		t.Error("AwaitEnd before any Play reported a clean end")
	}
}

func TestFCastAnswersWhenTheCastEnds(t *testing.T) {
	devicetest.AwaitsTheCastsEnd(t, func() device.Device {
		playing := newPlayback(stream)
		playing.loaded, playing.active = true, true
		return &session{ch: &channel{gone: make(chan struct{})}, current: playing}
	})
}

func TestCapabilitiesDeclareNoCaptionsSinceV3CannotCarryThem(t *testing.T) {
	caps := (&session{}).Capabilities()
	if err := protovalidate.Validate(caps); err != nil {
		t.Fatal(err)
	}
	if !caps.GetSelfFetch() {
		t.Error("FCast receivers fetch the URL they are handed, yet castor would relay every stream")
	}
	for _, f := range caps.GetFormats() {
		if len(f.GetCaptions()) > 0 || f.GetHls().GetSubtitles() != mediav1.HLSSupport_SUBTITLES_UNSPECIFIED {
			t.Errorf("%v promises subtitles a v3 Play has no field for, so they would never show", f)
		}
		if f.GetProgressive().GetContainer() == mediav1.ProgressiveSupport_CONTAINER_MPEGTS {
			t.Errorf("%v: Android receivers do not list MPEG-TS among the files they play", f)
		}
	}
}
