package media

import "errors"

// ErrNativeIncompatible means preserving the source requires a playback path that is not supported.
var ErrNativeIncompatible = errors.New("native playback cannot preserve this source on this receiver; disable preserve_source to allow conversion")
