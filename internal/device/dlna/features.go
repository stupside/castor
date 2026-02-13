package dlna

import (
	"fmt"

	"github.com/stupside/castor/internal/media"
)

// SupportedContentTypes lists the content types that DLNA renderers support.
var SupportedContentTypes = []string{"video/mp2t", media.MP4}

// profileName returns the DLNA profile name for a content type.
func profileName(contentType string) string {
	switch contentType {
	case "video/mp2t":
		return "MPEG_TS_HD_NA"
	case "video/mp4":
		return "AVC_MP4_HP_HD_AAC"
	default:
		return ""
	}
}

// ContentFeatures returns a DLNA content features string for use in HTTP
// headers and DIDL metadata.
func ContentFeatures(contentType string) string {
	pn := profileName(contentType)
	if pn != "" {
		return fmt.Sprintf("DLNA.ORG_PN=%s;DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=21700000000000000000000000000000", pn)
	}
	return "DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=21700000000000000000000000000000"
}

// StreamHeaders returns HTTP headers a DLNA renderer expects on a stream response.
func StreamHeaders(contentType string) map[string]string {
	return map[string]string{
		"Content-Length":           "9223372036854775807",
		"Accept-Ranges":            "none",
		"transferMode.dlna.org":    "Streaming",
		"contentFeatures.dlna.org": ContentFeatures(contentType),
	}
}
