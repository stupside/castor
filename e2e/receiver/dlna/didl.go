package dlna

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// didlProtocolInfo reads what the DIDL-Lite res declares (http-get:*:<mime>:<features>).
func didlProtocolInfo(meta string) []string {
	var didl struct {
		Res []struct {
			ProtocolInfo string `xml:"protocolInfo,attr"`
		} `xml:"item>res"`
	}
	if err := xml.Unmarshal([]byte(meta), &didl); err != nil {
		return nil
	}
	if len(didl.Res) == 0 {
		return nil
	}
	// The first res is the media; later ones are companions such as captions.
	fields := strings.SplitN(didl.Res[0].ProtocolInfo, ":", 4)
	if len(fields) < 3 {
		return nil
	}
	return fields
}

// didlCaptions are the URLs of the text tracks the DIDL-Lite offers beside the media.
func didlCaptions(meta string) []string {
	var didl struct {
		Res []struct {
			ProtocolInfo string `xml:"protocolInfo,attr"`
			URL          string `xml:",chardata"`
		} `xml:"item>res"`
	}
	if err := xml.Unmarshal([]byte(meta), &didl); err != nil {
		return nil
	}
	var urls []string
	for _, res := range didl.Res {
		if strings.Contains(res.ProtocolInfo, ":text/srt:") {
			urls = append(urls, res.URL)
		}
	}
	return urls
}

func didlContentType(meta string) string {
	fields := didlProtocolInfo(meta)
	if len(fields) < 3 {
		return ""
	}
	return fields[2]
}
func didlFeatures(meta string) string {
	fields := didlProtocolInfo(meta)
	if len(fields) < 4 || fields[3] == "*" {
		return ""
	}
	return fields[3]
}

// timeSeeks reads DLNA.ORG_OP, whose first digit offers fetching the stream from a position in time.
func timeSeeks(features string) bool {
	for param := range strings.SplitSeq(features, ";") {
		if op, ok := strings.CutPrefix(param, "DLNA.ORG_OP="); ok {
			return strings.HasPrefix(op, "1")
		}
	}
	return false
}

func timeSeekRange(at time.Duration) string { return fmt.Sprintf("npt=%.3f-", at.Seconds()) }
