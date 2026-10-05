package source

import (
	"iter"
	"strconv"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

var videoCodecEntries = map[string]media.Codec{
	"avc1": media.CodecH264,
	"avc3": media.CodecH264,
	"hvc1": media.CodecHEVC,
	"hev1": media.CodecHEVC,
	"av01": media.CodecAV1,
	"vp08": media.CodecVP8,
	"vp09": media.CodecVP9,
	// WebM's DASH profile names its codecs plainly rather than by sample entry.
	"vp8":  media.CodecVP8,
	"vp9":  media.CodecVP9,
	"mp4v": media.CodecMPEG4,
	"dvh1": "",
	"dvhe": "",
}

var declaredAudioEntries = map[string]media.Codec{
	"mp4a.40.2":  media.CodecAAC,
	"mp4a.40.02": media.CodecAAC,
	"mp4a.40.5":  media.CodecAAC,
	"mp4a.40.05": media.CodecAAC,
	"mp4a.40.29": media.CodecAAC,
	"mp4a.40.34": media.CodecMP3,
	"mp4a.69":    media.CodecMP3,
	"mp4a.6b":    media.CodecMP3,
	"ac-3":       media.CodecAC3,
	"ec-3":       media.CodecEAC3,
	"opus":       media.CodecOpus,
	"vorbis":     media.CodecVorbis,
}

var h264Profiles = map[uint64]media.Profile{
	0x42: media.ProfileBaseline,
	0x4d: media.ProfileMain,
	0x58: media.ProfileExtended,
	0x64: media.ProfileHigh,
}

// entries are the sample entries an RFC 6381 codecs list names, lowered and trimmed.
func entries(codecs string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for entry := range strings.SplitSeq(codecs, ",") {
			if entry = strings.ToLower(strings.TrimSpace(entry)); entry != "" && !yield(entry) {
				return
			}
		}
	}
}

// Family is the sample entry a codecs entry names, its parameters aside.
func Family(entry string) string { return sampleEntryName(strings.ToLower(strings.TrimSpace(entry))) }

// DeclaresVideo reports that an RFC 6381 codecs list names a video sample entry.
func DeclaresVideo(codecs string) bool {
	for entry := range entries(codecs) {
		if _, video := videoCodecEntries[sampleEntryName(entry)]; video {
			return true
		}
	}
	return false
}

// DeclaresSound reports that an RFC 6381 codecs list names anything but video.
func DeclaresSound(codecs string) bool {
	for entry := range entries(codecs) {
		if _, video := videoCodecEntries[sampleEntryName(entry)]; !video {
			return true
		}
	}
	return false
}

// DeclaredEnvelope is the codecs a list states for a picture of height, nil unless it names exactly one picture and at most one sound castor knows.
func DeclaredEnvelope(codecs string, height int) *media.ProbeInfo {
	if strings.TrimSpace(codecs) == "" {
		return nil
	}
	envelope := media.ProbeInfo{VideoHeight: height}
	video, audio := 0, 0
	for entry := range strings.SplitSeq(codecs, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if _, carriesPicture := videoCodecEntries[sampleEntryName(entry)]; carriesPicture {
			codec, profile, ok := declaredVideo(entry)
			if !ok {
				return nil
			}
			envelope.VideoCodec, envelope.VideoProfile, envelope.VideoBitDepth = codec, profile, 8
			envelope.VideoLevel = declaredLevel(entry)
			video++
			continue
		}
		codec, named := declaredAudioEntries[entry]
		if !named {
			return nil
		}
		envelope.AudioCodec = codec
		audio++
	}
	if video != 1 || audio > 1 {
		return nil
	}
	return &envelope
}

func declaredVideo(entry string) (media.Codec, media.Profile, bool) {
	codec := videoCodecEntries[sampleEntryName(entry)]
	_, params, stated := strings.Cut(entry, ".")
	if !stated {
		return "", "", false
	}
	switch codec {
	case media.CodecH264:
		profileIDC, constrained, ok := h264Params(params)
		if !ok {
			return "", "", false
		}
		profile, named := h264Profiles[profileIDC]
		if !named {
			return "", "", false
		}
		if profile == media.ProfileBaseline && constrained {
			profile = media.ProfileConstrainedBaseline
		}
		return codec, profile, true
	case media.CodecHEVC:
		if profileIDC, _, _ := strings.Cut(params, "."); profileIDC != "1" {
			return "", "", false
		}
		return codec, media.ProfileMain, true
	}
	return "", "", false
}

// h264Params reads RFC 6381's hex PPCCLL, or the decimal PP.LL early Apple tools and Wowza still write, which states no constraints.
func h264Params(params string) (profileIDC uint64, constrained, ok bool) {
	if profile, level, legacy := strings.Cut(params, "."); legacy {
		p, perr := strconv.ParseUint(profile, 10, 8)
		_, lerr := strconv.ParseUint(level, 10, 8)
		return p, false, perr == nil && lerr == nil
	}
	if len(params) != 6 {
		return 0, false, false
	}
	value, err := strconv.ParseUint(params, 16, 32)
	return value >> 16, value&0x004000 != 0, err == nil
}

func sampleEntryName(entry string) string {
	name, _, _ := strings.Cut(entry, ".")
	return name
}

// declaredLevel preserves the codec's level in ffprobe's units, after declaredVideo has checked the entry.
func declaredLevel(entry string) int {
	_, params, _ := strings.Cut(entry, ".")
	switch videoCodecEntries[sampleEntryName(entry)] {
	case media.CodecH264:
		if _, level, legacy := strings.Cut(params, "."); legacy {
			n, _ := strconv.Atoi(level)
			return n
		}
		n, _ := strconv.ParseUint(params[4:], 16, 8)
		return int(n)
	case media.CodecHEVC:
		for field := range strings.SplitSeq(params, ".") {
			if len(field) > 1 && (field[0] == 'l' || field[0] == 'h') {
				n, _ := strconv.Atoi(field[1:])
				return n
			}
		}
	}
	return 0
}
