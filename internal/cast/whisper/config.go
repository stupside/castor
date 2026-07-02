package whisper

// Config holds settings for the in-process whisper.cpp transcriber. It is
// the sole mechanism for enabling subtitle generation; there is no CLI
// override. Set Enable: true in config.yaml (or CASTOR_WHISPER__ENABLE=true)
// to opt in. Everything else is self-managed: the transcription and VAD
// models auto-download to the user cache, and the streaming pipeline needs
// no tuning.
type Config struct {
	Enable    bool   `yaml:"enable"`
	ModelPath string `yaml:"model_path"` // override the auto-downloaded tiny.en model
	Language  string `yaml:"language"`   // BCP-47; pin it — per-buffer auto-detection is unstable
}
