package receiver

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Measured is what landed on the tape, read by ffprobe and ffmpeg with no help from castor.
type Measured struct {
	Video  string
	Width  int
	Height int
	Depth  int
	// Chroma is the subsampling, 420, 422 or 444.
	Chroma int
	Level  int
	// Rotation is the display matrix's rotation in degrees, 0 when upright.
	Rotation int
	// FieldOrder is ffprobe's field_order: progressive, tt, bb, tb, bt or unknown.
	FieldOrder string
	// Combed is a decode seeing more interlaced frames than progressive ones.
	Combed bool
	// Tallest is the tallest keyframe on the tape, which catches a splice taller than the stream's header says.
	Tallest int
	// Transfer is ffprobe's color_transfer, empty when untagged.
	Transfer     string
	Audio        string
	Channels     int
	SampleRate   int
	Duration     time.Duration
	VideoPackets int
	AudioPackets int
	// VideoStart and AudioStart are each track's first timestamp; their gap is how far out of sync playback opens.
	VideoStart time.Duration
	AudioStart time.Duration
	// DecodeErrors is what a full decode of the tape complained about.
	DecodeErrors []string
	// LongestFreeze is the longest stretch between consecutive pictures, where a viewer sees the image stop.
	LongestFreeze time.Duration
}

type streamFacts struct {
	Type       string `json:"codec_type"`
	Codec      string `json:"codec_name"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Channels   int    `json:"channels"`
	SampleRate string `json:"sample_rate"`
	Packets    string `json:"nb_read_packets"`
	PixFmt     string `json:"pix_fmt"`
	RawBits    string `json:"bits_per_raw_sample"`
	Level      int    `json:"level"`
	FieldOrder string `json:"field_order"`
	Transfer   string `json:"color_transfer"`
	StartTime  string `json:"start_time"`
	SideData   []struct {
		Rotation int `json:"rotation"`
	} `json:"side_data_list"`
}

// takers fold each stream into the measurement: the tallest picture, as a device's player settles on it, and the first sound.
var takers = map[string]func(m *Measured, s streamFacts){
	"video": func(m *Measured, s streamFacts) {
		if m.Video != "" && s.Height <= m.Height {
			return
		}
		m.Video, m.Width, m.Height, m.Depth, m.Chroma = s.Codec, s.Width, s.Height, depth(s), chroma(s.PixFmt)
		m.Level, m.FieldOrder, m.Transfer, m.Rotation = s.Level, s.FieldOrder, s.Transfer, rotation(s)
		m.VideoPackets, m.VideoStart = atoi(s.Packets), seconds(s.StartTime)
	},
	"audio": func(m *Measured, s streamFacts) {
		if m.Audio != "" {
			return
		}
		m.Audio, m.Channels, m.SampleRate = s.Codec, s.Channels, atoi(s.SampleRate)
		m.AudioPackets, m.AudioStart = atoi(s.Packets), seconds(s.StartTime)
	},
}

// measure counts packets rather than trusting headers, since a stream header survives a muxer that dropped every packet.
func measure(ffmpeg, ffprobe, tape string) (Measured, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-count_packets",
		"-show_entries", "stream=codec_type,codec_name,width,height,channels,sample_rate,nb_read_packets,pix_fmt,bits_per_raw_sample,level,field_order,color_transfer,start_time:stream_side_data=rotation:format=duration",
		"-of", "json", tape).Output()
	if err != nil {
		return Measured{}, err
	}
	var report struct {
		Streams []streamFacts `json:"streams"`
		Format  struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		return Measured{}, err
	}
	m := Measured{Duration: seconds(report.Format.Duration)}
	for _, s := range report.Streams {
		if take, ok := takers[s.Type]; ok {
			take(&m, s)
		}
	}
	if m.Tallest, err = tallestKeyframe(ctx, ffprobe, tape); err != nil {
		return Measured{}, err
	}
	if m.LongestFreeze, err = longestFreeze(ctx, ffprobe, tape); err != nil {
		return Measured{}, err
	}
	m.DecodeErrors, m.Combed = decode(ctx, ffmpeg, tape, m.VideoPackets > 0)
	return m, nil
}

// tallestKeyframe decodes only keyframes, since a resolution change needs a new sequence header on an IDR.
func tallestKeyframe(ctx context.Context, ffprobe, tape string) (int, error) {
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v", "-skip_frame", "nokey",
		"-show_entries", "frame=height", "-of", "csv=p=0", tape).Output()
	if err != nil {
		return 0, fmt.Errorf("reading the tape's keyframe heights: %w", err)
	}
	tallest := 0
	for f := range strings.FieldsSeq(string(out)) {
		tallest = max(tallest, atoi(strings.TrimSuffix(f, ",")))
	}
	return tallest, nil
}

// longestFreeze walks the pictures in the order they were written, keeping the widest step forward past every one before it.
func longestFreeze(ctx context.Context, ffprobe, tape string) (time.Duration, error) {
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time", "-of", "csv=p=0", tape).Output()
	if err != nil {
		return 0, fmt.Errorf("reading the tape's picture times: %w", err)
	}
	var longest, reached time.Duration
	started := false
	for f := range strings.FieldsSeq(string(out)) {
		if f = strings.TrimSuffix(f, ","); f == "N/A" {
			continue
		}
		at := seconds(f)
		switch {
		// A clock that jumps back is a new run of timestamps, not a picture shown twice.
		case !started || at < reached-time.Second:
			reached, started = at, true
		case at > reached:
			longest, reached = max(longest, at-reached), at
		}
	}
	return longest, nil
}

// decode plays the whole tape through ffmpeg, keeping what it complains about and whether idet sees combing.
func decode(ctx context.Context, ffmpeg, tape string, video bool) ([]string, bool) {
	idet := tape + ".idet"
	args := []string{"-v", "error", "-i", tape}
	if video {
		args = append(args, "-map", "0:v:0?", "-map", "0:a:0?", "-vf", "idet,metadata=mode=print:key=lavfi.idet.multiple.current_frame:file="+idet)
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpeg, append(args, "-f", "null", "-")...)
	cmd.Stderr = &stderr
	err := cmd.Run()
	errs := strings.FieldsFunc(stderr.String(), func(r rune) bool { return r == '\n' })
	if err != nil {
		errs = append(errs, fmt.Sprintf("decoding the tape: %v", err))
	}
	printed, _ := os.ReadFile(idet)
	fields := string(printed)
	combed := strings.Count(fields, "=tff")+strings.Count(fields, "=bff") > strings.Count(fields, "=progressive")
	return errs, combed
}

// depth is the bit depth ffprobe reports, or reads off the pixel format's name (yuv420p10le is 10).
func depth(s streamFacts) int {
	if bits := atoi(s.RawBits); bits > 0 {
		return bits
	}
	for _, d := range []string{"12", "10"} {
		if strings.Contains(s.PixFmt, "p"+d) {
			return atoi(d)
		}
	}
	if s.PixFmt == "" {
		return 0
	}
	return 8
}

// chroma reads the subsampling off the pixel format's name; anything unnamed is 4:2:0.
func chroma(pixFmt string) int {
	for _, c := range []string{"444", "422"} {
		if strings.Contains(pixFmt, c) {
			return atoi(c)
		}
	}
	return 420
}

func rotation(s streamFacts) int {
	for _, d := range s.SideData {
		if d.Rotation != 0 {
			return d.Rotation
		}
	}
	return 0
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func seconds(s string) time.Duration {
	f, _ := strconv.ParseFloat(s, 64)
	return time.Duration(f * float64(time.Second))
}
