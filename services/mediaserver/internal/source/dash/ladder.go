package dash

import (
	"cmp"
	"slices"

	"github.com/Eyevinn/dash-mpd/mpd"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// offered is one representation a Period offers, with the set it inherits from.
type offered struct {
	set *mpd.AdaptationSetType
	rep *mpd.RepresentationType
}

func (o offered) height() int    { return height(o.set, o.rep) }
func (o offered) codecs() string { return codecs(o.set, o.rep) }

// muxed is a video representation whose segments carry its sound too.
func (o offered) muxed() bool {
	return kind(o.set, o.rep) == media.TrackVideo && source.DeclaresSound(o.codecs())
}

// offers is every representation of a kind a Period offers to cast, trick-play sets aside.
func offers(p *mpd.Period, k media.TrackKind) []offered {
	var out []offered
	for _, s := range p.AdaptationSets {
		if trickMode(s) {
			continue
		}
		for _, r := range s.Representations {
			if kind(s, r) == k {
				out = append(out, offered{set: s, rep: r})
			}
		}
	}
	return out
}

// feature is the Period a choice is made in: live, the one still open; else the longest, since ads are short and the film is what is chosen for.
func feature(periods []placed, live bool) placed {
	if live {
		return periods[len(periods)-1]
	}
	return slices.MaxFunc(periods, func(a, b placed) int {
		return cmp.Or(cmp.Compare(a.duration, b.duration), cmp.Compare(len(offers(a.Period, media.TrackVideo)), len(offers(b.Period, media.TrackVideo))), cmp.Compare(b.index, a.index))
	})
}

// ladder is the feature's video rungs, each weighing its sound too; a measurement only fills an omitted height, since ffprobe pairs them by position.
func ladder(p placed, probe *media.ProbeInfo, sound *offered) []source.Rendition {
	video := offers(p.Period, media.TrackVideo)
	rungs := make([]source.Rendition, len(video))
	for i, o := range video {
		bitrate := media.Bitrate(o.rep.Bandwidth)
		if sound != nil && !o.muxed() {
			bitrate += media.Bitrate(sound.rep.Bandwidth)
		}
		rungs[i] = source.Rendition{
			Representation: o.rep.Id,
			Height:         o.height(),
			Bitrate:        bitrate,
			Declared:       source.DeclaredEnvelope(o.codecs(), o.height()),
		}
	}
	// ffprobe numbers the first Period's representations in document order, so only then do the two pair by position.
	if probe != nil && p.index == 0 && len(probe.VideoHeights) == len(rungs) {
		for i := range rungs {
			if rungs[i].Height == 0 && probe.VideoHeights[i] > 0 {
				rungs[i].Height = probe.VideoHeights[i]
			}
		}
	}
	return rungs
}

// byHeight prefers the tallest rung, then the richer, then the one every device decodes: a rung inherits a height from its set, and twin encodes differ only by codec.
func byHeight(a, b source.Rendition) int {
	return cmp.Or(cmp.Compare(a.Height, b.Height), cmp.Compare(a.Bitrate, b.Bitrate), cmp.Compare(avc(a), avc(b)))
}

func avc(r source.Rendition) int {
	if r.Declared != nil && r.Declared.VideoCodec == media.CodecH264 {
		return 1
	}
	return 0
}

// audioRank orders sound a device is likeliest to decode first.
var audioRank = []string{"mp4a", "ec-3", "ac-3", "opus"}

func rank(codecs string) int {
	if i := slices.Index(audioRank, source.Family(codecs)); i >= 0 {
		return len(audioRank) - i
	}
	return 0
}

// byAudio prefers the main track, then the codec a device likeliest decodes, then the richer.
func byAudio(a, b offered) int {
	return cmp.Or(source.CompareBool(mainRole(a.set), mainRole(b.set)), cmp.Compare(rank(a.codecs()), rank(b.codecs())), cmp.Compare(a.rep.Bandwidth, b.rep.Bandwidth))
}

// preferredAudio chooses the first requested language the presentation offers.
// Its normal main/codec ranking applies within that language and when none match.
func preferredAudio(audio []offered, languages []string) offered {
	for _, language := range languages {
		matching := slices.DeleteFunc(slices.Clone(audio), func(o offered) bool {
			return !source.SameLanguage(o.set.Lang, language)
		})
		if len(matching) > 0 {
			return slices.MaxFunc(matching, byAudio)
		}
	}
	return slices.MaxFunc(audio, byAudio)
}
