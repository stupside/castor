package dlna

import (
	"encoding/xml"
	"fmt"
	"net/url"
)

type didlLite struct {
	XMLName xml.Name   `xml:"DIDL-Lite"`
	XMLNS   string     `xml:"xmlns,attr"`
	DC      string     `xml:"xmlns:dc,attr"`
	UPnP    string     `xml:"xmlns:upnp,attr"`
	Item    didlItem   `xml:"item"`
}

type didlItem struct {
	ID         string   `xml:"id,attr"`
	ParentID   string   `xml:"parentID,attr"`
	Restricted string   `xml:"restricted,attr"`
	Title      string   `xml:"dc:title"`
	Class      string   `xml:"upnp:class"`
	Res        didlRes  `xml:"res"`
}

type didlRes struct {
	ProtocolInfo string `xml:"protocolInfo,attr"`
	Value        string `xml:",chardata"`
}

func buildDIDLMetadata(streamURL *url.URL, contentType string) (string, error) {
	protocolInfo := fmt.Sprintf(
		"http-get:*:%s:DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=01700000000000000000000000000000",
		contentType,
	)

	item := didlLite{
		XMLNS: "urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/",
		DC:    "http://purl.org/dc/elements/1.1/",
		UPnP:  "urn:schemas-upnp-org:metadata-1-0/upnp/",
		Item: didlItem{
			ID:         "0",
			ParentID:   "-1",
			Restricted: "1",
			Title:      "Castor Stream",
			Class:      "object.item.videoItem",
			Res: didlRes{
				ProtocolInfo: protocolInfo,
				Value:        streamURL.String(),
			},
		},
	}

	data, err := xml.Marshal(item)
	if err != nil {
		return "", fmt.Errorf("marshaling DIDL-Lite: %w", err)
	}

	return string(data), nil
}
