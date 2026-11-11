package mediaserver

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/settings"
)

func TestDesktopToolPathsStillUseInstalledNames(t *testing.T) {
	want := FFmpegConfig{Path: "ffmpeg", ProbePath: "ffprobe"}
	if got := defaults().Servers.Media.FFmpeg; !reflect.DeepEqual(got, want) {
		t.Fatalf("default tool paths = %#v, want %#v", got, want)
	}
}

func TestConfiguredToolPaths(t *testing.T) {
	want := FFmpegConfig{Path: "/opt/castor/ffmpeg", ProbePath: "/opt/castor/ffprobe"}
	t.Setenv("CASTOR_SERVERS__MEDIA__FFMPEG__PATH", want.Path)
	t.Setenv("CASTOR_SERVERS__MEDIA__FFMPEG__PROBE_PATH", want.ProbePath)
	cmd := &cli.Command{Flags: []cli.Flag{settings.Flag}}
	cmd.Action = func(_ context.Context, cmd *cli.Command) error {
		got, err := settings.Load(cmd, defaults())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Servers.Media.FFmpeg, want) {
			t.Fatalf("tool paths = %#v, want %#v", got.Servers.Media.FFmpeg, want)
		}
		return nil
	}
	if err := cmd.Run(t.Context(), []string{"media"}); err != nil {
		t.Fatal(err)
	}
}

func TestTheHostDeclaresItsHardwareEncodersByName(t *testing.T) {
	for _, tt := range []struct {
		declared string
		want     []string
		refused  bool
	}{
		{"hevc_mediacodec,h264_mediacodec", []string{"hevc_mediacodec", "h264_mediacodec"}, false},
		{"h264_qsv", []string{"h264_qsv"}, false},
		{"libx264", nil, true},
		{"h264_bogus", nil, true},
	} {
		t.Setenv("CASTOR_SERVERS__MEDIA__FFMPEG__HARDWARE_ENCODERS", tt.declared)
		cmd := &cli.Command{Flags: []cli.Flag{settings.Flag}}
		cmd.Action = func(_ context.Context, cmd *cli.Command) error {
			got, err := settings.Load(cmd, defaults())
			if tt.refused {
				if err == nil {
					t.Errorf("%s was accepted", tt.declared)
				}
				return nil
			}
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range got.Servers.Media.FFmpeg.HardwareEncoders {
				names = append(names, e.Name)
			}
			if !reflect.DeepEqual(names, tt.want) {
				t.Errorf("declared %s, got %v", tt.declared, names)
			}
			return nil
		}
		if err := cmd.Run(t.Context(), []string{"media"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAConfigFileDeclaresHardwareEncodersAsAList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("servers:\n  media:\n    ffmpeg:\n      hardware_encoders: [h264_mediacodec]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := &cli.Command{
		Name: "castor", Flags: []cli.Flag{settings.Flag},
		Action: func(_ context.Context, cmd *cli.Command) error {
			got, err := settings.Load(cmd, defaults())
			if err != nil {
				return err
			}
			if declared := got.Servers.Media.FFmpeg.HardwareEncoders; len(declared) != 1 || declared[0].Name != "h264_mediacodec" {
				t.Errorf("declared %v, want h264_mediacodec", declared)
			}
			return nil
		},
	}
	if err := command.Run(t.Context(), []string{"castor", "--config", path}); err != nil {
		t.Fatal(err)
	}
}

func TestWhisperConfigBelongsToMediaServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("servers:\n  media:\n    whisper:\n      model_path: /models/from-file.bin\n    resolver:\n      probe_max_concurrency: 3\n    network:\n      interface: eth0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	load := func() *Config {
		t.Helper()
		var got *Config
		command := &cli.Command{
			Name: "castor", Flags: []cli.Flag{settings.Flag},
			Action: func(_ context.Context, cmd *cli.Command) (err error) {
				got, err = settings.Load(cmd, defaults())
				return err
			},
		}
		if err := command.Run(context.Background(), []string{"castor", "--config", path}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := load().Servers.Media.Whisper.ModelPath; got != "/models/from-file.bin" {
		t.Errorf("model path from file = %q", got)
	}
	if got := load().Servers.Media; got.Resolver.ProbeMaxConcurrency != 3 || got.Network.Interface != "eth0" {
		t.Errorf("nested media settings = %+v", got)
	}
	t.Setenv("CASTOR_SERVERS__MEDIA__WHISPER__MODEL_PATH", "/models/from-env.bin")
	if got := load().Servers.Media.Whisper.ModelPath; got != "/models/from-env.bin" {
		t.Errorf("model path from environment = %q", got)
	}
}
