// Package remoteapi exposes a small, opt-in HTTPS API for the Android companion.
package remoteapi

import (
    "context"
    "crypto/subtle"
    "crypto/tls"
    "encoding/json"
    "fmt"
    "net/http"
    "strings"
    "time"
    "sxops/internal/core"
)

type component struct {
    Kind string `json:"kind"`
    Name string `json:"name"`
    State string `json:"state"`
    Running bool `json:"running"`
    Version string `json:"version,omitempty"`
    Address string `json:"address,omitempty"`
    OnionHost string `json:"onionHost,omitempty"`
}
type statusResponse struct { Components []component `json:"components"` }
type result struct { OK bool `json:"ok"`; Error string `json:"error,omitempty"` }

// Handler exposes only status and global start/stop operations.
func Handler(engine *core.Engine, token string) http.Handler {
    mux := http.NewServeMux()
    protect := func(method string, action http.HandlerFunc) http.HandlerFunc {
        return func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("Cache-Control", "no-store")
            w.Header().Set("X-Content-Type-Options", "nosniff")
            if r.Method != method { w.Header().Set("Allow", method); writeJSON(w, http.StatusMethodNotAllowed, result{Error: "method not allowed"}); return }
            supplied := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
            if len(supplied) != len(token) || subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) != 1 { writeJSON(w, http.StatusUnauthorized, result{Error: "unauthorized"}); return }
            action(w, r)
        }
    }
    mux.HandleFunc("/api/v1/status", protect(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
        out := statusResponse{Components: []component{}}
        for _, s := range engine.Statuses() { out.Components = append(out.Components, component{Kind: string(s.Kind), Name: s.Name, State: s.StateText, Running: s.Running, Version: s.Version, Address: s.Address, OnionHost: s.OnionHost}) }
        writeJSON(w, http.StatusOK, out)
    }))
    mux.HandleFunc("/api/v1/start", protect(http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
        ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute); defer cancel()
        if err := engine.StartAll(ctx); err != nil { writeJSON(w, http.StatusInternalServerError, result{Error: err.Error()}); return }
        writeJSON(w, http.StatusOK, result{OK: true})
    }))
    mux.HandleFunc("/api/v1/stop", protect(http.MethodPost, func(w http.ResponseWriter, r *http.Request) { engine.StopAll(false); writeJSON(w, http.StatusOK, result{OK: true}) }))
    return mux
}

// ServeTLS requires HTTPS and a long random bearer token.
func ServeTLS(addr, certFile, keyFile, token string, engine *core.Engine) error {
    if len(token) < 32 || strings.TrimSpace(token) != token { return fmt.Errorf("remote API token must be at least 32 characters and contain no surrounding whitespace") }
    if certFile == "" || keyFile == "" { return fmt.Errorf("TLS certificate and key are required") }
    srv := &http.Server{Addr: addr, Handler: Handler(engine, token), ReadHeaderTimeout: 5*time.Second, IdleTimeout: 60*time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
    return srv.ListenAndServeTLS(certFile, keyFile)
}

func writeJSON(w http.ResponseWriter, code int, value any) {
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    w.WriteHeader(code)
    _ = json.NewEncoder(w).Encode(value)
}
