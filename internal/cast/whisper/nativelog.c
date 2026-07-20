#include <whisper.h>

#include "_cgo_export.h"

// Forward only warnings and errors to Go; drop INFO/DEBUG/CONT (the VAD
// per-segment chatter) at the C boundary so it never crosses into Go.
static void castorLogBridge(enum ggml_log_level level, const char *text, void *user_data) {
	(void)user_data;
	if (level == GGML_LOG_LEVEL_WARN || level == GGML_LOG_LEVEL_ERROR) {
		castorNativeLog((int)level, (char *)text);
	}
}

void castorInstallLogBridge(void) {
	ggml_log_set(castorLogBridge, NULL);
	whisper_log_set(castorLogBridge, NULL);
}
