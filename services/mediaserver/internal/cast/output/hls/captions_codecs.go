package hls

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/Eyevinn/mp4ff/aac"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
)

// renditionCodecs describes the produced bitstream, including copied HEVC/AV1,
// rather than relying on a receiver's default assumption of AVC and AAC.
func renditionCodecs(movie *mp4.MoovBox) (string, error) {
	codecs, err := mediaCodecs(movie)
	if err != nil {
		return "", err
	}
	return codecs + ",wvtt", nil
}

func mediaCodecs(movie *mp4.MoovBox) (string, error) {
	var codecs []string
	for _, track := range movie.Traks {
		if track.Mdia == nil || track.Mdia.Minf == nil || track.Mdia.Minf.Stbl == nil || track.Mdia.Minf.Stbl.Stsd == nil {
			return "", fmt.Errorf("missing sample description")
		}
		stsd := track.Mdia.Minf.Stbl.Stsd
		switch {
		case stsd.AvcX != nil && stsd.AvcX.AvcC != nil:
			entry := stsd.AvcX
			codecs = append(codecs, fmt.Sprintf("%s.%02x%02x%02x", entry.Type(), entry.AvcC.AVCProfileIndication, entry.AvcC.ProfileCompatibility, entry.AvcC.AVCLevelIndication))
		case stsd.HvcX != nil && stsd.HvcX.HvcC != nil:
			entry := stsd.HvcX
			nalus := entry.HvcC.GetNalusForType(hevc.NALU_SPS)
			if len(nalus) == 0 {
				return "", fmt.Errorf("missing HEVC SPS")
			}
			sps, err := hevc.ParseSPSNALUnit(nalus[0])
			if err != nil {
				return "", err
			}
			codecs = append(codecs, hevc.CodecString(entry.Type(), sps))
		case stsd.Av01 != nil && stsd.Av01.Av1C != nil:
			codecs = append(codecs, stsd.Av01.Av1C.CodecString("av01"))
		case stsd.VpXX != nil && stsd.VpXX.VppC != nil:
			entry := stsd.VpXX
			codecs = append(codecs, fmt.Sprintf("%s.%02d.%02d.%02d", entry.Type(), entry.VppC.Profile, entry.VppC.Level, entry.VppC.BitDepth))
		case stsd.Mp4a != nil && stsd.Mp4a.Esds != nil && stsd.Mp4a.Esds.DecConfigDescriptor != nil:
			config := stsd.Mp4a.Esds.DecConfigDescriptor
			codec := fmt.Sprintf("mp4a.%02x", config.ObjectType)
			if config.ObjectType == mp4.ObjectTypeAAC {
				if config.DecSpecificInfo == nil {
					return "", fmt.Errorf("missing AAC configuration")
				}
				asc, err := aac.DecodeAudioSpecificConfigHeader(bytes.NewReader(config.DecSpecificInfo.DecConfig))
				if err != nil {
					return "", err
				}
				codec += fmt.Sprintf(".%d", asc.ObjectType)
			}
			codecs = append(codecs, codec)
		case stsd.AC3 != nil:
			codecs = append(codecs, "ac-3")
		case stsd.EC3 != nil:
			codecs = append(codecs, "ec-3")
		case stsd.Opus != nil:
			codecs = append(codecs, "opus")
		default:
			return "", fmt.Errorf("unsupported HLS sample description")
		}
	}
	return strings.Join(codecs, ","), nil
}
