package dlna

import (
	"net/url"
	"strings"
	"testing"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

func TestADeviceIsAnnouncedTheStreamAsItsOwnSinkEntryDescribesIt(t *testing.T) {
	const sink = "http-get:*:video/mpeg:DLNA.ORG_PN=MPEG_PS_PAL," +
		"http-get:*:video/vnd.dlna.mpeg-tts:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO;DLNA.ORG_OP=01;DLNA.ORG_CI=0," +
		"http-get:*:video/mp4:*"
	_, sinks := parseSinkProtocolInfo(sink)
	stream := &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/stream"}
	for container, want := range map[mediav1.Container]string{
		mediav1.Container_CONTAINER_MPEGTS: "http-get:*:video/vnd.dlna.mpeg-tts:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO;DLNA.ORG_OP=01;DLNA.ORG_CI=0",
		mediav1.Container_CONTAINER_MP4:    "http-get:*:video/mp4:*",
	} {
		metadata, err := buildDIDLMetadata(stream, sinks[container], nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(metadata, `protocolInfo="`+want+`"`) {
			t.Errorf("%v was announced as %s, want protocolInfo %q", container, metadata, want)
		}
	}
}

func TestTheServedHeadersCarryTheSinkEntryNotAGuess(t *testing.T) {
	_, sinks := parseSinkProtocolInfo("http-get:*:video/mp2t:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO;DLNA.ORG_OP=01,http-get:*:video/mp4:*")
	if got := servedHeaders(sinks[mediav1.Container_CONTAINER_MPEGTS])["contentFeatures.dlna.org"]; got != "DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO;DLNA.ORG_OP=01" {
		t.Errorf("contentFeatures.dlna.org = %q, want the device's own", got)
	}
	if _, ok := servedHeaders(sinks[mediav1.Container_CONTAINER_MP4])["contentFeatures.dlna.org"]; ok {
		t.Error("an entry with no features was announced with a contentFeatures header")
	}
}

func TestACaptionIsAnnouncedBesideTheStreamTheWayRenderersReadIt(t *testing.T) {
	_, sinks := parseSinkProtocolInfo("http-get:*:video/mp4:*")
	stream := &url.URL{Scheme: "http", Host: "192.0.2.1:8080", Path: "/stream"}
	caption := &mediav1.DeviceCommand_Caption{Url: "http://192.0.2.1:8080/en.srt", Format: mediav1.CaptionFormat_CAPTION_FORMAT_SRT}
	metadata, err := buildDIDLMetadata(stream, sinks[mediav1.Container_CONTAINER_MP4], caption, nil)
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
	metadata, err := buildDIDLMetadata(stream, sinks[mediav1.Container_CONTAINER_MP4], nil, info)
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
