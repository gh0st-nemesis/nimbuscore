package dashboard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookieName = "nimbus_session"
	sessionTTL        = 24 * time.Hour
)

// sessionAuth replaces raw HTTP Basic Auth with a signed session cookie, so
// the dashboard can present a real login page instead of the browser's
// native credentials prompt. The signing secret is generated fresh at
// process start (crypto/rand) and never persisted — a restart simply
// invalidates every outstanding session, which is an acceptable trade-off
// for a small single-admin dashboard and keeps this dependency-free.
type sessionAuth struct {
	username string
	password string
	secret   []byte
	static   fs.FS
}

func newSessionAuth(username, password string, static fs.FS) *sessionAuth {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("dashboard: failed to generate session secret: " + err.Error())
	}
	return &sessionAuth{username: username, password: password, secret: secret, static: static}
}

// issueToken returns "<expiryUnix>.<base64url(HMAC-SHA256(secret, expiryUnix))>".
func (s *sessionAuth) issueToken() string {
	expiry := strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(expiry)) //nolint:errcheck
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return expiry + "." + sig
}

func (s *sessionAuth) validToken(token string) bool {
	expiryStr, sigB64, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(expiryStr)) //nolint:errcheck
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return false
	}
	expiry, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < expiry
}

func (s *sessionAuth) isAuthenticated(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return s.validToken(cookie.Value)
}

// requireSession protects everything except the login page and its API. API
// requests get a plain 401 (so the frontend's fetch wrapper can react);
// everything else is redirected to the login page.
func (s *sessionAuth) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

func (s *sessionAuth) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	userMatch := subtle.ConstantTimeCompare([]byte(req.Username), []byte(s.username)) == 1
	passMatch := subtle.ConstantTimeCompare([]byte(req.Password), []byte(s.password)) == 1
	if !userMatch || !passMatch {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    s.issueToken(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *sessionAuth) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *sessionAuth) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	b, err := fs.ReadFile(s.static, "login.html")
	if err != nil {
		http.Error(w, "login page not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b) //nolint:errcheck
}
