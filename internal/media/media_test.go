package media

import "testing"

func TestStreamInfoPlayable(t *testing.T) {
	cases := []struct {
		name string
		info StreamInfo
		want bool
	}{
		{"video+audio", StreamInfo{HasVideo: true, HasAudio: true}, true},
		{"video only", StreamInfo{HasVideo: true}, false},
		{"audio only", StreamInfo{HasAudio: true}, false},
		{"neither (image decoy)", StreamInfo{}, false},
	}
	for _, c := range cases {
		if got := c.info.Playable(); got != c.want {
			t.Errorf("%s: Playable() = %v, want %v", c.name, got, c.want)
		}
	}
}
