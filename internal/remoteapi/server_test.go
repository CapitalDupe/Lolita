package remoteapi
import ("net/http"; "net/http/httptest"; "testing")
func TestStatusRequiresBearerToken(t *testing.T) {
 h := Handler(nil, "a-long-test-token-that-is-at-least-32-chars")
 rec := httptest.NewRecorder(); h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
 if rec.Code != http.StatusUnauthorized { t.Fatalf("got %d, want 401", rec.Code) }
}
func TestStatusRejectsWrongMethod(t *testing.T) {
 token := "a-long-test-token-that-is-at-least-32-chars"; h := Handler(nil, token)
 req := httptest.NewRequest(http.MethodPost, "/api/v1/status", nil); req.Header.Set("Authorization", "Bearer "+token)
 rec := httptest.NewRecorder(); h.ServeHTTP(rec, req)
 if rec.Code != http.StatusMethodNotAllowed { t.Fatalf("got %d, want 405", rec.Code) }
}
