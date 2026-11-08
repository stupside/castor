package wire

import (
	"buf.build/go/protovalidate"
	"testing"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

func TestAStreamKeepsTheCaptionsGivenWithIt(t *testing.T) {
	got, err := FromStream(&castorv1.Stream{Url: "https://origin.example/film.mkv", Captions: []*castorv1.Caption{{Url: "https://origin.example/film.fr.vtt", Language: "fre", Name: "Français"}}})
	if err != nil {
		t.Fatal(err)
	}
	if c := got.Captions; len(c) != 1 || c[0].URL.String() != "https://origin.example/film.fr.vtt" || c[0].Language != "fre" || c[0].Name != "Français" {
		t.Errorf("captions %+v, want the French track as given", c)
	}
}

func TestAStreamsCaptionsAreHeldToTheContract(t *testing.T) {
	caption := func(url, language string) *castorv1.Stream {
		return &castorv1.Stream{Url: "https://origin.example/film.mkv", Captions: []*castorv1.Caption{{Url: url, Language: language}}}
	}
	for name, tc := range map[string]struct {
		stream *castorv1.Stream
		valid  bool
	}{
		"tagged":             {caption("https://origin.example/film.fr.vtt", "fre"), true},
		"untagged":           {caption("https://origin.example/film.vtt", ""), true},
		"region tagged":      {caption("https://origin.example/film.vtt", "en-US"), false},
		"not served by HTTP": {caption("ftp://origin.example/film.vtt", "en"), false},
	} {
		if err := protovalidate.Validate(tc.stream); (err == nil) != tc.valid {
			t.Errorf("%s: validation said %v, want valid %v", name, err, tc.valid)
		}
	}
}
