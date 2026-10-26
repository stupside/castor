package suite

import (
	"github.com/stupside/castor/e2e/command"
	"github.com/stupside/castor/e2e/command/decoy"
	"github.com/stupside/castor/e2e/command/offer"
	"github.com/stupside/castor/e2e/judge"
	"github.com/stupside/castor/e2e/judge/expect"
	"github.com/stupside/castor/e2e/judge/invariant"
	"github.com/stupside/castor/e2e/judge/outcome"
	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/codec"
	"github.com/stupside/castor/e2e/origin/colour"
	"github.com/stupside/castor/e2e/origin/dash"
	"github.com/stupside/castor/e2e/origin/file"
	"github.com/stupside/castor/e2e/origin/hls"
	"github.com/stupside/castor/e2e/origin/quirk"
	"github.com/stupside/castor/e2e/origin/serving/access"
	"github.com/stupside/castor/e2e/origin/serving/faults"
	"github.com/stupside/castor/e2e/origin/serving/playlist"
	"github.com/stupside/castor/e2e/origin/serving/presentation"
	"github.com/stupside/castor/e2e/receiver"
	"github.com/stupside/castor/e2e/receiver/chromecast"
	"github.com/stupside/castor/e2e/receiver/dlna"
	"github.com/stupside/castor/e2e/receiver/player"
	"github.com/stupside/castor/e2e/receiver/roku"
	"github.com/stupside/castor/e2e/receiver/viewer"
	"github.com/stupside/castor/e2e/settings"
	"github.com/stupside/castor/e2e/strategy"
)

// Every strategy a case can name, and the checks every played cast answers to.
var (
	catalog = origin.Catalog{
		Packagers: strategy.Registry[origin.Packager]{
			hls.TS{}, hls.FMP4{}, hls.Short{}, hls.SingleFile{}, hls.AES128{},
			dash.Packager{}, dash.Time{}, dash.SingleFile{}, dash.Duration{}, dash.WebM{},
			file.MP4, file.MP4MoovLast, file.MKV, file.WebM, file.TS,
		},
		Video: strategy.Registry[origin.VideoCodec]{codec.H264{}, codec.HEVC{}, codec.VP8{}, codec.VP9{}, codec.AV1{}},
		Audio: strategy.Registry[origin.AudioCodec]{
			codec.AAC{}, codec.AC3{}, codec.EAC3{}, codec.Opus{}, codec.FLAC{}, codec.MP3{}, codec.Vorbis{},
		},
		Transfers: strategy.Registry[origin.Transfer]{colour.SDR{}, colour.PQ{}, colour.HLG{}},
		Quirks: strategy.Registry[strategy.Factory[origin.Quirk]]{
			quirk.Rotated{}, quirk.FrameRate{}, quirk.Interlaced{}, quirk.Chroma{},
			quirk.AudioDelay{}, quirk.SampleRate{}, quirk.PTSWrapAfter{}, quirk.Weighs{},
		},
	}
	behaviours = strategy.Registry[strategy.Factory[origin.Behaviour]]{
		playlist.Live{},
		playlist.Event{},
		playlist.RestartsAt{},
		playlist.SplicesAd{},
		playlist.RewritesMaster{},
		playlist.AlternateAudio{},
		playlist.SourceCaptions{},
		playlist.AudioLanguages{},
		presentation.DashLive{},
		presentation.Periods{},
		faults.Trickles{},
		faults.StallsAt{},
		faults.StallsFor{},
		faults.HangsOnce{},
		faults.Hiccups{},
		faults.ColdEdge{},
		faults.Truncates{},
		access.SignsSegments{},
		faults.Redirects{},
		playlist.StaleEdge{},
		presentation.DashAdPeriod{},
		strategy.Fixed[origin.Behaviour]{Strategy: access.SessionCookie{}},
		strategy.Fixed[origin.Behaviour]{Strategy: faults.IgnoresRange{}},
		strategy.Fixed[origin.Behaviour]{Strategy: access.RefusesSegments{}},
		strategy.Fixed[origin.Behaviour]{Strategy: faults.Tarpit{}},
		strategy.Fixed[origin.Behaviour]{Strategy: access.RequiresReferer{}},
		strategy.Fixed[origin.Behaviour]{Strategy: access.RotatesCookie{}},
		strategy.Fixed[origin.Behaviour]{Strategy: presentation.SegmentBase{}},
		strategy.Fixed[origin.Behaviour]{Strategy: presentation.RenamesPerPeriod{}},
		strategy.Fixed[origin.Behaviour]{Strategy: playlist.ImplicitIV{}},
	}
	commands = strategy.Registry[strategy.Factory[command.Command]]{
		strategy.Fixed[command.Command]{Strategy: command.URL{}},
		command.Player{
			Offers: strategy.Registry[strategy.Factory[command.Offer]]{
				strategy.Fixed[command.Offer]{Strategy: command.Fetched},
				strategy.Fixed[command.Offer]{Strategy: offer.Withheld{}},
				offer.Delayed{}, offer.Logged{}, offer.Framed{},
			},
			Decoys: strategy.Registry[command.Decoy]{decoy.AdClip{}, decoy.EmptyPlaylist{}, decoy.Blob{}, decoy.Image{}},
		},
		command.Movie{},
		command.Episode{},
	}
	carriers   = strategy.Registry[settings.Carrier]{settings.File{}, settings.Env{}}
	topologies = strategy.Registry[topology]{embedded{}, split{}}
	families   = strategy.Registry[receiver.Family]{dlna.Family{}, chromecast.Family{}, roku.Family{}}
	// players are tried in order; File takes anything, so it is last.
	players = strategy.Registry[receiver.Player]{player.Playlist{}, player.File{}}
	viewers = strategy.Registry[strategy.Factory[receiver.Viewer]]{
		strategy.Fixed[receiver.Viewer]{Strategy: viewer.ToTheEnd{}},
		viewer.StopsAfter{},
		viewer.Takes{},
		viewer.Seeks{},
	}
	outcomes     = strategy.Registry[judge.Outcome]{outcome.Plays{}, outcome.Refused{}, outcome.Aborted{}}
	expectations = strategy.Registry[strategy.Factory[judge.Check]]{
		strategy.OneOf[judge.Check]{Called: "delivery", Of: strategy.Registry[judge.Check]{expect.Served{}, expect.Passthrough{}}},
		strategy.OneOf[judge.Check]{Called: "video", Of: strategy.Registry[judge.Check]{expect.Copied{}, expect.Encoded{}, expect.Smallest{}}},
		expect.Channels{},
		expect.Captioned{},
		expect.AudioIn{},
		strategy.Fixed[judge.Check]{Strategy: expect.Uninterrupted{}},
		strategy.Fixed[judge.Check]{Strategy: expect.Seeked{}},
		strategy.OneOf[judge.Check]{Called: "frames", Of: strategy.Registry[judge.Check]{expect.Complete{}}},
	}
	invariants = []judge.Check{
		invariant.Clean{}, invariant.PictureLanded{}, invariant.Upright{}, invariant.Progressive{}, invariant.Level{},
		invariant.SoundFollowsSource{}, invariant.InSync{}, invariant.PlayedOut{}, invariant.Unfrozen{},
	}
)
