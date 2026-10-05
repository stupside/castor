package probe

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

const showEntries = "format=format_name,bit_rate,duration,start_time:" +
	"stream=index,codec_type,codec_name,profile,level,width,height,pix_fmt,color_transfer,field_order,channels,sample_rate:" +
	"stream_disposition=attached_pic:stream_side_data=rotation:" +
	"program=program_id:program_stream_disposition=attached_pic:" +
	"frame=stream_index,interlaced_frame"

func decode(out []byte, videoIndex, audioIndex int) (media.ProbeInfo, error) {
	var result struct {
		Streams []struct {
			Index         int    `json:"index"`
			CodecType     string `json:"codec_type"`
			CodecName     string `json:"codec_name"`
			Profile       string `json:"profile"`
			Width         int    `json:"width"`
			Height        int    `json:"height"`
			PixFmt        string `json:"pix_fmt"`
			ColorTransfer string `json:"color_transfer"`
			Level         int    `json:"level"`
			FieldOrder    string `json:"field_order"`
			Channels      int    `json:"channels"`
			SampleRate    string `json:"sample_rate"`
			Disposition   struct {
				AttachedPic int `json:"attached_pic"`
			} `json:"disposition"`
			SideData []struct {
				Rotation int `json:"rotation"`
			} `json:"side_data_list"`
		} `json:"streams"`
		Programs []struct {
			ProgramID int `json:"program_id"`
			Streams   []struct {
				CodecType   string `json:"codec_type"`
				Height      int    `json:"height"`
				Disposition struct {
					AttachedPic int `json:"attached_pic"`
				} `json:"disposition"`
			} `json:"streams"`
		} `json:"programs"`
		Frames []probedFrame `json:"frames"`
		Format struct {
			FormatName string `json:"format_name"`
			BitRate    string `json:"bit_rate"`
			Duration   string `json:"duration"`
			StartTime  string `json:"start_time"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return media.ProbeInfo{}, fmt.Errorf("parsing ffprobe output: %w", err)
	}
	if result.Format.FormatName == "" {
		return media.ProbeInfo{}, fmt.Errorf("ffprobe returned no format name")
	}

	info := media.ProbeInfo{ContentType: formatToContentType(result.Format.FormatName)}
	// Non-numeric rate left at zero; no decision turns on it alone.
	info.BitRate, _ = strconv.ParseInt(result.Format.BitRate, 10, 64)
	if secs, err := strconv.ParseFloat(result.Format.Duration, 64); err == nil && secs > 0 {
		info.Duration = time.Duration(secs * float64(time.Second))
	}
	if secs, err := strconv.ParseFloat(result.Format.StartTime, 64); err == nil {
		info.Start = time.Duration(secs * float64(time.Second))
	}

	videoSeen, audioSeen := 0, 0
	for _, s := range result.Streams {
		switch s.CodecType {
		case "video":
			if s.Disposition.AttachedPic != 0 {
				continue
			}
			info.VideoHeights = append(info.VideoHeights, s.Height)
			if videoSeen == videoIndex {
				// Dimensionless selected stream occupies real map index but establishes no picture envelope.
				if s.Width > 0 && s.Height > 0 {
					info.VideoCodec = media.Codec(s.CodecName)
					info.VideoProfile = media.Profile(s.Profile)
					info.VideoHeight = s.Height
					info.VideoBitDepth = pixFmtBitDepth(s.PixFmt)
					info.VideoHDR = isHDRTransfer(s.ColorTransfer)
					info.VideoLevel = s.Level
					info.VideoInterlaced = interlaced[s.FieldOrder] || slices.ContainsFunc(result.Frames, func(f probedFrame) bool {
						return f.StreamIndex == s.Index && f.InterlacedFrame == 1
					})
					for _, d := range s.SideData {
						info.VideoRotation = cmp.Or(info.VideoRotation, d.Rotation)
					}
				}
			}
			videoSeen++
		case "audio":
			if audioSeen == audioIndex {
				info.AudioCodec = media.Codec(s.CodecName)
				info.AudioChannels = s.Channels
				info.AudioSampleRate, _ = strconv.Atoi(s.SampleRate)
			}
			audioSeen++
		}
	}
	if len(result.Programs) > 0 {
		info.ProgramHeights = make(map[int]int, len(result.Programs))
	}
	for _, program := range result.Programs {
		tallest := 0
		for _, s := range program.Streams {
			if s.CodecType == "video" && s.Disposition.AttachedPic == 0 {
				tallest = max(tallest, s.Height)
			}
		}
		info.ProgramHeights[program.ProgramID] = tallest
	}
	return info, nil
}

// pixelDepth is a component depth followed by an endian marker; digits in nv12 or yuv420p describe layout instead.
var pixelDepth = regexp.MustCompile(`(9|10|12|14|16|32)(?:le|be)$`)

// pixFmtBitDepth reads component depth, rather than the number of bits in a packed RGB pixel.
func pixFmtBitDepth(pixFmt string) int {
	if depth := pixelDepth.FindStringSubmatch(pixFmt); depth != nil {
		n, _ := strconv.Atoi(depth[1])
		return n
	}
	switch {
	case pixFmt == "":
		return 0
	case strings.HasPrefix(pixFmt, "rgb48"), strings.HasPrefix(pixFmt, "bgr48"), strings.HasPrefix(pixFmt, "rgba64"), strings.HasPrefix(pixFmt, "bgra64"):
		return 16
	default:
		return 8
	}
}

// probedFrame is one decoded frame: whether it was coded as fields is only known once one decodes.
type probedFrame struct {
	StreamIndex     int `json:"stream_index"`
	InterlacedFrame int `json:"interlaced_frame"`
}

// interlaced is every field order ffprobe reports for a picture coded as fields.
var interlaced = map[string]bool{"tt": true, "bb": true, "tb": true, "bt": true}

func isHDRTransfer(transfer string) bool {
	switch transfer {
	case "smpte2084", "arib-std-b67":
		return true
	default:
		return false
	}
}

// formatToContentType maps an ffprobe format_name to a content type.
func formatToContentType(format string) string {
	for f := range strings.SplitSeq(format, ",") {
		switch strings.TrimSpace(f) {
		case ffmpeg.FormatHLS, "applehttp":
			return media.HLS
		case ffmpeg.FormatDASH:
			return media.DASH
		// "mp4" is checked but "mov" deliberately is not: ffprobe reports the same joined list for both.
		case ffmpeg.FormatMP4:
			return media.MP4
		case "matroska":
			return media.MKV
		case "webm":
			return media.WebM
		case "avi":
			return media.AVI
		case ffmpeg.FormatMPEGTS:
			return media.MPEGTS
		case "flv":
			return media.FLV
		}
	}
	return ""
}
