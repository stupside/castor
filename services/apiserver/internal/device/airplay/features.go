package airplay

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// features are the AirPlay feature bits a receiver announces.
type features uint64

// The bits castor reads, as pyatv and the AirPlay 2 references number them.
const (
	featureVideoV1        = 0  // plays a URL handed to /play
	featureUnifiedControl = 38 // AirPlay 2 media control
	featureSystemPairing  = 43
	featureHomeKitPairing = 46
	featureCoreUtils      = 48 // AirPlay 2 pairing and encryption
	featureVideoV2        = 49 // plays a URL queued on a remote control stream
)

func (f features) has(bit uint) bool { return f&(1<<bit) != 0 }

var featureWords = regexp.MustCompile(`^0x([0-9A-Fa-f]{1,8})(?:,0x([0-9A-Fa-f]{1,8}))?$`)

// parseFeatures reads a features TXT value: one 32-bit word, or the low word then the high word.
// Some receivers drop the low word's leading zeros (LG announces 0x7F8AD0,0xB8BCF46), so each word is read on its own.
func parseFeatures(s string) (features, error) {
	m := featureWords.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("invalid AirPlay features %q", s)
	}
	low, _ := strconv.ParseUint(m[1], 16, 32)
	var high uint64
	if m[2] != "" {
		high, _ = strconv.ParseUint(m[2], 16, 32)
	}
	return features(high<<32 | low), nil
}

// parseFeaturesEx reads fex, the same bits as a little-endian bitmap in base64, often without padding.
func parseFeaturesEx(s string) (features, error) {
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return 0, fmt.Errorf("invalid AirPlay fex %q: %w", s, err)
	}
	var f features
	for i, b := range raw {
		if i >= 8 {
			break
		}
		f |= features(b) << (8 * i)
	}
	return f, nil
}

// method is how castor hands a receiver a URL to play.
type method int

const (
	methodNone method = iota
	// methodVideoV1 posts the URL to /play, as Apple TVs take it.
	methodVideoV1
	// methodVideoV2 queues the URL on a remote control stream, as third-party TVs take it.
	methodVideoV2
)

func (m method) String() string {
	switch m {
	case methodVideoV1:
		return "video v1"
	case methodVideoV2:
		return "video v2"
	}
	return "no video"
}

// videoMethod prefers /play where a receiver has both, as pyatv does.
func (f features) videoMethod() method {
	switch {
	case f.has(featureVideoV1):
		return methodVideoV1
	case f.has(featureVideoV2):
		return methodVideoV2
	}
	return methodNone
}

// airPlay2 is whether the receiver sets sessions up the AirPlay 2 way: pair-verified SETUP, event channel and RECORD.
func (f features) airPlay2() bool { return f.has(featureUnifiedControl) || f.has(featureCoreUtils) }

// transient is whether the receiver may accept a pairing without a PIN.
func (f features) transient() bool { return f.has(featureSystemPairing) || f.has(featureCoreUtils) }

// Status flags a receiver announces in sf, flags or /info's statusFlags.
const (
	statusPINRequired     = 0x8
	statusPasswordNeeded  = 0x80
	statusPairingRequired = 0x200
)

// unsupportedModel names the receivers castor knows it cannot cast video to: a Mac answers AirPlay but refuses third-party senders.
var unsupportedModel = regexp.MustCompile(`^Mac\d+,\d+$`)
