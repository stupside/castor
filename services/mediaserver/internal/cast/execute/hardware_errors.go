package execute

import (
	"slices"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
)

// An ffmpeg that reads the source and encodes the picture in one process, as a read or a remux does, exits on the
// source's failure as on the encoder's, so an exit alone blames the encoder only for a signal. A hardware encoder
// refuses pictures software makes, such as MediaCodec a small one, and says so in its own error lines, written under
// its own context; those lines make the exit the encoder's, so the cast is made again in software instead of ending.

// hardwareFailed is a read whose hardware video encoder logged its own failure.
func (p *pull) hardwareFailed() bool {
	t := p.tail()
	encode, ok := t.floor.Video.Encode()
	if !ok || !encode.Encoder.Hardware {
		return false
	}
	return failedItself(encode.Encoder.Name, t.proc.Evidence().Lines)
}

// failedItself is a log with errors from the encoder named name, not from the input it read or the muxer it fed.
func failedItself(name string, lines []string) bool {
	return slices.ContainsFunc(lines, func(line string) bool {
		erring, ok := erringEncoder(line)
		return ok && erring == name
	})
}

// hardwareFailedItself is a log with errors from a hardware encoder castor knows.
func hardwareFailedItself(lines []string) bool {
	return slices.ContainsFunc(lines, func(line string) bool {
		erring, ok := erringEncoder(line)
		return ok && ffmpeg.HardwareEncoder(erring)
	})
}

// erringEncoder is the encoder whose own context wrote line as an error: "[enc:name @ …]" or a leading "[name @ …]".
func erringEncoder(line string) (string, bool) {
	if !strings.Contains(line, "] [error] ") && !strings.Contains(line, "] [fatal] ") {
		return "", false
	}
	if _, after, ok := strings.Cut(line, "[enc:"); ok {
		name, _, ok := strings.Cut(after, " @")
		return name, ok
	}
	if rest, ok := strings.CutPrefix(line, "["); ok {
		name, _, ok := strings.Cut(rest, " @")
		return name, ok
	}
	return "", false
}
