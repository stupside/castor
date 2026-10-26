package dlna

import (
	"cmp"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

// captionKinds names a caption format the way DLNA renderers do, in a protocolInfo's text/ type.
var captionKinds = map[mediav1.CaptionFormat]string{
	mediav1.CaptionFormat_CAPTION_FORMAT_SRT: "srt",
	mediav1.CaptionFormat_CAPTION_FORMAT_VTT: "vtt",
}

// streamed has every progressive format served with DLNA's streaming transfer headers.
func streamed(caps *mediav1.Capabilities) {
	for _, f := range caps.GetFormats() {
		if progressive := f.GetProgressive(); progressive != nil {
			progressive.ServedHeaders = map[string]string{
				"Connection":            "close",
				"Accept-Ranges":         "none",
				"transferMode.dlna.org": "Streaming",
			}
		}
	}
}

// streamingFlags are DLNA 1.5's streaming and background modes, with connection stalling so a paused device keeps its connection.
const streamingFlags = "DLNA.ORG_FLAGS=01700000000000000000000000000000"

// stated is the protocolInfo fourth field castor gives a stream its sink entry describes, fetched as seek says.
func stated(entry sinkEntry, play *mediav1.DeviceCommand_Play) string {
	switch {
	case play.GetSource() != nil:
		// Castor does not serve it, so it states only what the device itself said.
		return entry.features
	case play.GetPlaylist() != nil:
		// No DLNA parameter describes a playlist.
		return "*"
	}
	var params []string
	for param := range strings.SplitSeq(entry.features, ";") {
		if strings.HasPrefix(param, "DLNA.ORG_PN=") {
			params = append(params, param)
			break
		}
	}
	op := "DLNA.ORG_OP=00"
	if play.GetProgressive().GetRuntime().GetSeek() == mediav1.DeviceCommand_Play_Runtime_SEEK_TIME {
		op = "DLNA.ORG_OP=10"
	}
	return strings.Join(append(params, op, streamingFlags), ";")
}

type didlLite struct {
	XMLName xml.Name `xml:"DIDL-Lite"`
	XMLNS   string   `xml:"xmlns,attr"`
	DC      string   `xml:"xmlns:dc,attr"`
	UPnP    string   `xml:"xmlns:upnp,attr"`
	Item    didlItem `xml:"item"`
}

type didlItem struct {
	ID          string    `xml:"id,attr"`
	ParentID    string    `xml:"parentID,attr"`
	Restricted  string    `xml:"restricted,attr"`
	Title       string    `xml:"dc:title"`
	Description string    `xml:"dc:description,omitempty"`
	Date        string    `xml:"dc:date,omitempty"`
	AlbumArtURI string    `xml:"upnp:albumArtURI,omitempty"`
	Class       string    `xml:"upnp:class"`
	Res         []didlRes `xml:"res"`
}

type didlRes struct {
	ProtocolInfo string `xml:"protocolInfo,attr"`
	Value        string `xml:",chardata"`
	Duration     string `xml:"duration,attr,omitempty"`
}

// buildDIDLMetadata announces playback as the device's own sink entry describes it, with its caption beside it.
func buildDIDLMetadata(entry sinkEntry, playback device.Playback) (string, error) {
	item := didlLite{
		XMLNS: "urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/",
		DC:    "http://purl.org/dc/elements/1.1/",
		UPnP:  "urn:schemas-upnp-org:metadata-1-0/upnp/",
		Item: didlItem{
			ID:          "0",
			ParentID:    "-1",
			Restricted:  "1",
			Title:       cmp.Or(playback.Info.GetTitle(), "Castor Stream"),
			Description: playback.Info.GetSynopsis(),
			Date:        playback.Info.GetReleaseDate(),
			AlbumArtURI: playback.Info.GetImageUrl(),
			Class:       "object.item.videoItem",
			Res: []didlRes{{
				ProtocolInfo: fmt.Sprintf("http-get:*:%s:%s", entry.mime, stated(entry, playback.Play)),
				Value:        playback.URL.String(),
				Duration:     clock(playback.Duration()),
			}},
		},
	}
	if caption := shownCaption(playback.Play.GetCaptions()); caption != nil {
		item.Item.Res = append(item.Item.Res, didlRes{ProtocolInfo: "http-get:*:text/" + captionKinds[caption.GetFormat()] + ":*", Value: caption.GetUrl()})
	}

	data, err := xml.Marshal(item)
	if err != nil {
		return "", fmt.Errorf("marshaling DIDL-Lite: %w", err)
	}
	return string(data), nil
}

func clock(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	s := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}
