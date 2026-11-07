package mediaserver

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/stupside/castor/internal/settings"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
)

func TestDesktopToolPathsStillUseInstalledNames(t *testing.T) {
	want := FFmpegConfig{Path: "ffmpeg", ProbePath: "ffprobe"}
	if got := defaults().Servers.Media.FFmpeg; !reflect.DeepEqual(got, want) {
		t.Fatalf("default tool paths = %#v, want %#v", got, want)
	}
}

func TestHardwareEncodersAreDeclaredByThePlatformOnly(t *testing.T) {
	if got := defaults().Servers.Media.FFmpeg.HardwareEncoders; len(got) != 0 {
		t.Fatalf("a host declares no hardware encoders by default, got %v", got)
	}
	load := func(declared string) (*Config, error) {
		t.Setenv("CASTOR_SERVERS__MEDIA__FFMPEG__HARDWARE_ENCODERS", declared)
		var cfg *Config
		var loadErr error
		cmd := &cli.Command{Flags: []cli.Flag{settings.Flag}, Action: func(_ context.Context, cmd *cli.Command) error {
			cfg, loadErr = settings.Load(cmd, defaults())
			return nil
		}}
		if err := cmd.Run(t.Context(), []string{"media"}); err != nil {
			t.Fatal(err)
		}
		return cfg, loadErr
	}
	cfg, err := load("hevc_mediacodec,h264_mediacodec")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Servers.Media.FFmpeg.HardwareEncoders; !slices.Equal(got, []string{"hevc_mediacodec", "h264_mediacodec"}) {
		t.Fatalf("declared encoders = %v", got)
	}
	if _, err := load("h264_imaginary"); err == nil {
		t.Fatal("an encoder castor does not know must fail at startup, not be ignored")
	}
}

func TestEveryDeclarableEncoderCanBeConfigured(t *testing.T) {
	field, _ := reflect.TypeFor[FFmpegConfig]().FieldByName("HardwareEncoders")
	_, names, _ := strings.Cut(field.Tag.Get("validate"), "oneof=")
	got := strings.Fields(names)
	slices.Sort(got)
	if want := ffmpeg.Declarable(); !slices.Equal(got, want) {
		t.Fatalf("the configuration admits %v, the encoder catalog knows %v", got, want)
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
