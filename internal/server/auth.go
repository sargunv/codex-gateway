package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

func authenticate(r *http.Request, want string) bool {
	a := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(a), "bearer ") {
		a = strings.TrimSpace(a[7:])
	}
	x := strings.TrimSpace(r.Header.Get("x-api-key"))
	if a != "" && x != "" && a != x {
		return false
	}
	got := a
	if got == "" {
		got = x
	}
	gh, wh := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	return got != "" && subtle.ConstantTimeCompare(gh[:], wh[:]) == 1
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(r, s.apiKey) {
			writeError(w, s.familyForPath(r.URL.Path), http.StatusUnauthorized, "authentication_error", "invalid gateway API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}
