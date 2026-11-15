package devicetest

import (
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
)

// Source is a play of the source itself, packaged as c.
func Source(c mediav1.Container) *mediav1.DeviceCommand_Play {
	return &mediav1.DeviceCommand_Play{Stream: &mediav1.DeviceCommand_Play_Source_{Source: &mediav1.DeviceCommand_Play_Source{Container: c}}}
}

// Served is a play of a stream the media server serves as c, of runtime where it is not zero.
func Served(c mediav1.Container, runtime time.Duration, seek mediav1.DeviceCommand_Play_Runtime_Seek) *mediav1.DeviceCommand_Play {
	served := &mediav1.DeviceCommand_Play_Progressive{Container: c}
	if runtime > 0 {
		served.Timeline = &mediav1.DeviceCommand_Play_Progressive_Runtime{Runtime: &mediav1.DeviceCommand_Play_Runtime{Duration: durationpb.New(runtime), Seek: seek}}
	}
	return &mediav1.DeviceCommand_Play{Stream: &mediav1.DeviceCommand_Play_Progressive_{Progressive: served}}
}

// Playlist is a play of an HLS playlist the media server serves, cut into segments.
func Playlist(segments mediav1.Container) *mediav1.DeviceCommand_Play {
	return &mediav1.DeviceCommand_Play{Stream: &mediav1.DeviceCommand_Play_Playlist_{Playlist: &mediav1.DeviceCommand_Play_Playlist{Segments: segments}}}
}

// Live is play as a stream with no end, whichever way it reaches the device.
func Live(play *mediav1.DeviceCommand_Play) *mediav1.DeviceCommand_Play {
	live := &mediav1.DeviceCommand_Play_Live{}
	switch s := play.GetStream().(type) {
	case *mediav1.DeviceCommand_Play_Source_:
		s.Source.Length = &mediav1.DeviceCommand_Play_Source_Live{Live: live}
	case *mediav1.DeviceCommand_Play_Progressive_:
		s.Progressive.Timeline = &mediav1.DeviceCommand_Play_Progressive_Live{Live: live}
	case *mediav1.DeviceCommand_Play_Playlist_:
		s.Playlist.Length = &mediav1.DeviceCommand_Play_Playlist_Live{Live: live}
	}
	return play
}
