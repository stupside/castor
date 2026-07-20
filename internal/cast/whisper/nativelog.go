package whisper

// whisper.cpp and ggml log through a global C callback that, by default, prints
// everything to stderr, including a torrent of per-segment VAD lines and a
// cumulative "vad time" counter that reads like a slowdown but isn't. Route
// that callback through slog and keep only warnings and errors, so native
// output joins the app's log stream at a sane volume instead of drowning it.

// #include <whisper.h>
// void castorInstallLogBridge(void); // defined in nativelog.c
import "C"

import (
	"context"
	"log/slog"
	"strings"
)

//export castorNativeLog
func castorNativeLog(level C.int, text *C.char) {
	msg := strings.TrimRight(C.GoString(text), "\r\n ")
	if msg == "" {
		return
	}
	// This is a global C callback fired on arbitrary threads (even before any
	// Run), so there is no request context to carry: Background keeps it
	// consistent with the codebase's *Context logging without inventing one.
	if level == 4 { // GGML_LOG_LEVEL_ERROR
		slog.ErrorContext(context.Background(), "whisper native", "text", msg)
	} else {
		slog.WarnContext(context.Background(), "whisper native", "text", msg)
	}
}

func init() { C.castorInstallLogBridge() }
