package dashboard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func newTestSessionAuth() *sessionAuth {
	return newSessionAuth("admin", "s3cret", fstest.MapFS{
		"login.html": &fstest.MapFile{Data: []byte("<html>login</html>")},
	})
}

func TestSessionTokenRoundTrip(t *testing.T) {
	s := newTestSessionAuth()
	token := s.issueToken()
	if !s.validToken(token) {
		t.Fatal("a freshly issued token was rejected")
	}
}

func TestSessionTokenRejectsExpired(t *testing.T) {
	s := newTestSessionAuth()
	// Sign an already-expired timestamp using the exact same scheme as issueToken,
	// exercising via the package-internal secret (this test lives in package dashboard).
	expiry := strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(expiry))
	token := expiry + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if s.validToken(token) {
		t.Fatal("an expired token was accepted")
	}
}

func TestSessionTokenRejectsTamperedSignature(t *testing.T) {
	s := newTestSessionAuth()
	token := s.issueToken()
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected token shape: %q", token)
	}
	tampered := parts[0] + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if s.validToken(tampered) {
		t.Fatal("a token with a tampered signature was accepted")
	}
}

func TestSessionTokenRejectsGarbage(t *testing.T) {
	s := newTestSessionAuth()
	for _, bad := range []string{"", "no-dot-here", "123.not-base64!!", "..", "123."} {
		if s.validToken(bad) {
			t.Errorf("validToken(%q) = true, want false", bad)
		}
	}
}

func TestRequireSessionReturns401ForAPIWithoutCookie(t *testing.T) {
	s := newTestSessionAuth()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := httptest.NewServer(s.requireSession(next))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/nodes")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestRequireSessionRedirectsNonAPIWithoutCookie(t *testing.T) {
	s := newTestSessionAuth()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	srv := httptest.NewServer(s.requireSession(next))
	defer srv.Close()

	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Errorf("location = %q, want /login", loc)
	}
}

func TestRequireSessionAllowsValidCookie(t *testing.T) {
	s := newTestSessionAuth()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := httptest.NewServer(s.requireSession(next))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/nodes", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: s.issueToken()})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
