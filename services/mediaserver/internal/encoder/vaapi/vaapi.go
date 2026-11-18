// Package vaapi is the hardware encoders of the render nodes on Linux.
package vaapi

import (
	"path/filepath"

	"github.com/stupside/castor/services/mediaserver/internal/encoder"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

const defaultRenderNode = "/dev/dri/renderD128"

// Family is VA-API's H.264 and HEVC encoders on the default render node, then on every other this host has.
type Family struct{}

func (Family) Encoders() []encoder.Encoder {
	nodes, _ := filepath.Glob("/dev/dri/renderD*")
	return encodersOnNodes(nodes)
}

// encodersOnNodes is the encoders of the default render node, then of each other node of nodes.
func encodersOnNodes(nodes []string) []encoder.Encoder {
	out := encodersOnNode(defaultRenderNode)
	for _, node := range nodes {
		if node != defaultRenderNode {
			out = append(out, encodersOnNode(node)...)
		}
	}
	return out
}

func encodersOnNode(node string) []encoder.Encoder {
	const alias = "va"
	initArgs := []string{"-init_hw_device", "vaapi=" + alias + ":" + node, "-filter_hw_device", alias}
	filters := []string{"format=nv12", "hwupload"}
	return []encoder.Encoder{
		{Name: "h264_vaapi", Codec: media.CodecH264, Hardware: true, InitArgs: initArgs, Filters: filters},
		{Name: "hevc_vaapi", Codec: media.CodecHEVC, Hardware: true, InitArgs: initArgs, Filters: filters},
	}
}
