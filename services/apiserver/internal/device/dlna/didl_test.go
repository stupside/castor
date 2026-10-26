package dlna

import (
	"net/url"
	"strings"
	"testing"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
	"github.com/stupside/castor/services/apiserver/internal/device/devicetest"
)

// flags are what castor states of a stream it serves: streaming and background transfer under DLNA 1.5.
const flags = ";DLNA.ORG_FLAGS=01500000000000000000000000000000"

func TestADeviceIsAnnouncedTheProfileItNamedAndHowCastorServesTheStream(t *testing.T) {
	const sink = "http-get:*:video/mpeg:DLNA.ORG_PN=MPEG_PS_PAL," +
		"http-get:*:video/vnd.dlna.mpeg-tts:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO;DLNA.ORG_OP=01;DLNA.ORG_CI=0," +
		"http-get:*:video/mp4:*"
	_, sinks := parseSinkProtocolInfo(sink)
	stream := &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/stream"}
	timed, from := mediav1.DeviceCommand_Play_Runtime_SEEK_TIME, mediav1.DeviceCommand_Play_Runtime_SEEK_START
	for _, tt := range []struct {
		container mediav1.Container
		play      *mediav1.DeviceCommand_Play
		want      string
	}{
		// A stream castor does not serve is announced as the device's own sink entry describes it.
		{mediav1.Container_CONTAINER_MPEGTS, devicetest.Source(mediav1.Container_CONTAINER_MPEGTS), "http-get:*:video/vnd.dlna.mpeg-tts:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO;DLNA.ORG_OP=01;DLNA.ORG_CI=0"},
		{mediav1.Container_CONTAINER_MP4, devicetest.Source(mediav1.Container_CONTAINER_MP4), "http-get:*:video/mp4:*"},
		{mediav1.Container_CONTAINER_MPEGTS, devicetest.Served(mediav1.Container_CONTAINER_MPEGTS, 0, 0), "http-get:*:video/vnd.dlna.mpeg-tts:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO;DLNA.ORG_OP=00" + flags},
		{mediav1.Container_CONTAINER_MPEGTS, devicetest.Served(mediav1.Container_CONTAINER_MPEGTS, time.Hour, from), "http-get:*:video/vnd.dlna.mpeg-tts:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO;DLNA.ORG_OP=00" + flags},
		{mediav1.Container_CONTAINER_MPEGTS, devicetest.Served(mediav1.Container_CONTAINER_MPEGTS, time.Hour, timed), "http-get:*:video/vnd.dlna.mpeg-tts:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO;DLNA.ORG_OP=10" + flags},
		{mediav1.Container_CONTAINER_MP4, devicetest.Served(mediav1.Container_CONTAINER_MP4, 0, 0), "http-get:*:video/mp4:DLNA.ORG_OP=00" + flags},
		{mediav1.Container_CONTAINER_MP4, devicetest.Served(mediav1.Container_CONTAINER_MP4, time.Hour, timed), "http-get:*:video/mp4:DLNA.ORG_OP=10" + flags},
	} {
		metadata, err := buildDIDLMetadata(sinks[tt.container], device.Playback{URL: stream, Play: tt.play})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(metadata, `protocolInfo="`+tt.want+`"`) {
			t.Errorf("%v was announced as %s, want protocolInfo %q", tt.play, metadata, tt.want)
		}
	}
}

func TestAProgressiveStreamIsServedWithTheDLNAStreamingHeaders(t *testing.T) {
	progressive := func(c mediav1.ProgressiveSupport_Container) *mediav1.FormatSupport {
		return &mediav1.FormatSupport{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: c}}}
	}
	caps := &mediav1.Capabilities{Formats: []*mediav1.FormatSupport{progressive(mediav1.ProgressiveSupport_CONTAINER_MPEGTS), progressive(mediav1.ProgressiveSupport_CONTAINER_MP4)}}
	streamed(caps)
	for _, f := range caps.Formats {
		if p := f.GetProgressive(); p != nil && p.GetServedHeaders()["transferMode.dlna.org"] != "Streaming" {
			t.Errorf("%v is served with %v, want transferMode Streaming", p.GetContainer(), p.GetServedHeaders())
		}
	}
}

func TestACaptionIsAnnouncedBesideTheStreamTheWayRenderersReadIt(t *testing.T) {
	_, sinks := parseSinkProtocolInfo("http-get:*:video/mp4:*")
	stream := &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/stream"}
	caption := &mediav1.DeviceCommand_Caption{Url: "http://192.0.2.1:8080/en.srt", Format: mediav1.CaptionFormat_CAPTION_FORMAT_SRT}
	metadata, err := buildDIDLMetadata(sinks[mediav1.Container_CONTAINER_MP4], device.Playback{URL: stream, Play: &mediav1.DeviceCommand_Play{Captions: []*mediav1.DeviceCommand_Caption{caption}}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `protocolInfo="http-get:*:text/srt:*">http://192.0.2.1:8080/en.srt</res>`; !strings.Contains(metadata, want) {
		t.Errorf("metadata = %s, want it to contain %s", metadata, want)
	}
}

func TestSelectedMovieDetailsAreAnnouncedToDLNAReceiver(t *testing.T) {
	_, sinks := parseSinkProtocolInfo("http-get:*:video/mp4:*")
	stream := &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/stream"}
	info := &castorv1.MediaInfo{Title: "A & B", Synopsis: "A story <on screen>", ImageUrl: "https://image.tmdb.org/t/p/w500/poster.jpg", ReleaseDate: "2025-03-01"}
	metadata, err := buildDIDLMetadata(sinks[mediav1.Container_CONTAINER_MP4], device.Playback{URL: stream, Info: info})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<dc:title>A &amp; B</dc:title>`,
		`<dc:description>A story &lt;on screen&gt;</dc:description>`,
		`<dc:date>2025-03-01</dc:date>`,
		`<upnp:albumArtURI>https://image.tmdb.org/t/p/w500/poster.jpg</upnp:albumArtURI>`,
	} {
		if !strings.Contains(metadata, want) {
			t.Errorf("metadata = %s, want %s", metadata, want)
		}
	}
}

func TestClock(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "", 90 * time.Minute: "1:30:00", 7325*time.Second + 400*time.Millisecond: "2:02:05"} {
		if got := clock(d); got != want {
			t.Errorf("clock(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestAStreamServedFromAnyTimeIsAnnouncedSeekableOverItsRuntime(t *testing.T) {
	_, sinks := parseSinkProtocolInfo("http-get:*:video/mp4:*")
	stream := &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/stream"}
	metadata, err := buildDIDLMetadata(sinks[mediav1.Container_CONTAINER_MP4], device.Playback{URL: stream, Play: devicetest.Served(mediav1.Container_CONTAINER_MP4, 102*time.Minute, mediav1.DeviceCommand_Play_Runtime_SEEK_TIME)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`DLNA.ORG_OP=10`, `duration="1:42:00"`} {
		if !strings.Contains(metadata, want) {
			t.Errorf("metadata = %s, want %s", metadata, want)
		}
	}
}
