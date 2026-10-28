package core

// newPlatformBackend returns the native backend. sxops runs as a native
// Linux application (inside WSL2 when run on Windows - see README
// for the one-time setup).
func newPlatformBackend(e *Engine) Backend {
	return &nativeBackend{eng: e}
}
