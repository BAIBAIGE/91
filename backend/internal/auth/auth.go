package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/video-site/backend/internal/catalog"
)

const (
	sessionCookie      = "vs_admin"
	sessionTTL         = 7 * 24 * time.Hour
	sessionRenewBefore = sessionTTL / 2
	loginFailWindow    = 30 * time.Minute
	loginFailThreshold = 3
)

var ErrLoginIPBanned = errors.New("login ip banned")
var ErrUserBanned = errors.New("user is banned")

type Authenticator struct {
	Username string
	Password string
	Catalog  *catalog.Catalog
	Now      func() time.Time

	credMu   sync.RWMutex
	mu       sync.Mutex
	failures map[string]loginFailure
}

type loginFailure struct {
	Count int
	First time.Time
}

type sessionIdentityContextKey struct{}

// SessionIdentityFromContext returns an opaque, server-only identity for the
// authenticated login session. The raw session token is never exposed to
// downstream handlers or retained by their in-memory state.
func SessionIdentityFromContext(ctx context.Context) (string, bool) {
	identity, ok := ctx.Value(sessionIdentityContextKey{}).(string)
	return identity, ok && identity != ""
}

func (a *Authenticator) Login(w http.ResponseWriter, r *http.Request, user, pass string) (bool, error) {
	expectedUser, expectedPass := a.Credentials()
	ip := clientIP(r)
	if ip != "" {
		banned, err := a.Catalog.IsLoginIPBanned(r.Context(), ip)
		if err != nil {
			return false, err
		}
		if banned {
			return false, ErrLoginIPBanned
		}
	}
	if subtle.ConstantTimeCompare([]byte(user), []byte(expectedUser)) != 1 ||
		subtle.ConstantTimeCompare([]byte(pass), []byte(expectedPass)) != 1 {
		if ip != "" {
			if err := a.recordFailure(r, ip); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	if ip != "" {
		a.clearFailures(ip)
	}
	token, err := randomToken()
	if err != nil {
		return false, err
	}
	expiresAt := a.now().Add(sessionTTL)
	if err := a.Catalog.CreateSessionUntil(r.Context(), token, expiresAt, 0); err != nil {
		return false, err
	}
	setSessionCookie(w, token, expiresAt)
	return true, nil
}

func (a *Authenticator) Credentials() (string, string) {
	a.credMu.RLock()
	defer a.credMu.RUnlock()
	return a.Username, a.Password
}

func (a *Authenticator) SetCredentials(username, password string) {
	a.credMu.Lock()
	defer a.credMu.Unlock()
	a.Username = username
	a.Password = password
}

// CheckCurrentPassword re-authenticates the administrator represented by the
// request's current session. Database-backed administrators are checked
// against their bcrypt hash; a legacy user_id=0 session is checked against the
// configured administrator password.
func (a *Authenticator) CheckCurrentPassword(r *http.Request, password string) (bool, error) {
	if a == nil || a.Catalog == nil || r == nil {
		return false, nil
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			return false, nil
		}
		return false, err
	}
	session, found, err := a.Catalog.GetSession(r.Context(), cookie.Value)
	if err != nil || !found {
		return false, err
	}
	if !a.now().Before(session.ExpiresAt) {
		return false, nil
	}
	if session.UserID == 0 {
		_, expected := a.Credentials()
		return subtle.ConstantTimeCompare([]byte(password), []byte(expected)) == 1, nil
	}
	user, err := a.Catalog.GetUserByID(r.Context(), session.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if user.Banned || user.Role != "admin" {
		return false, nil
	}
	return checkPassword(password, user.Password), nil
}

func (a *Authenticator) recordFailure(r *http.Request, ip string) error {
	now := a.now()
	a.mu.Lock()
	if a.failures == nil {
		a.failures = make(map[string]loginFailure)
	}
	f := a.failures[ip]
	if f.First.IsZero() || now.Sub(f.First) > loginFailWindow {
		f = loginFailure{First: now}
	}
	f.Count++
	a.failures[ip] = f
	shouldBan := f.Count >= loginFailThreshold
	a.mu.Unlock()

	if !shouldBan {
		return nil
	}
	if err := a.Catalog.BanLoginIP(r.Context(), ip, "too many failed login attempts"); err != nil {
		return err
	}
	return ErrLoginIPBanned
}

func (a *Authenticator) clearFailures(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, ip)
}

func (a *Authenticator) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Authenticator) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = a.Catalog.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:    sessionCookie,
		Value:   "",
		Path:    "/",
		Expires: time.Unix(0, 0),
	})
}

func (a *Authenticator) ValidateRequest(w http.ResponseWriter, r *http.Request) (bool, int64, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false, 0, nil
	}
	return a.validateSession(w, r, c.Value)
}

func (a *Authenticator) validateSession(w http.ResponseWriter, r *http.Request, token string) (bool, int64, error) {
	session, found, err := a.Catalog.GetSession(r.Context(), token)
	if err != nil || !found {
		return false, 0, err
	}
	now := a.now()
	if !now.Before(session.ExpiresAt) {
		return false, 0, nil
	}
	if session.ExpiresAt.Sub(now) < sessionRenewBefore {
		expiresAt := now.Add(sessionTTL)
		if err := a.Catalog.UpdateSessionExpires(r.Context(), token, expiresAt); err != nil {
			return false, 0, err
		}
		setSessionCookie(w, token, expiresAt)
	}
	return true, session.UserID, nil
}

func (a *Authenticator) Required(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, userID, err := a.ValidateRequest(w, r)
		if err != nil || !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if userID > 0 {
			u, err := a.Catalog.GetUserByID(r.Context(), userID)
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if u.Banned {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, withSessionIdentity(r))
	})
}

func withSessionIdentity(r *http.Request) *http.Request {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return r
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	identity := hex.EncodeToString(digest[:])
	return r.WithContext(context.WithValue(r.Context(), sessionIdentityContextKey{}, identity))
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func clientIP(r *http.Request) string {
	remote := remoteIP(r.RemoteAddr)
	if remote.IsValid() && isTrustedProxy(remote) {
		if ip := forwardedClientIP(r.Header.Get("X-Forwarded-For")); ip != "" {
			return ip
		}
		if ip := parseIPHeader(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
	}
	if remote.IsValid() {
		return remote.String()
	}
	return ""
}

func remoteIP(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		if ip, err := netip.ParseAddr(strings.TrimSpace(host)); err == nil {
			return ip.Unmap()
		}
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(remoteAddr))
	if err != nil {
		return netip.Addr{}
	}
	return ip.Unmap()
}

func isTrustedProxy(ip netip.Addr) bool {
	return ip.Unmap().IsLoopback()
}

func forwardedClientIP(header string) string {
	parts := forwardedIPs(header)
	for i := len(parts) - 1; i >= 0; i-- {
		if ip := parseIPHeader(parts[i]); ip != "" {
			return ip
		}
	}
	return ""
}

func parseIPHeader(value string) string {
	ip, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return ip.Unmap().String()
}

func forwardedIPs(header string) []string {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	parts := strings.Split(header, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

func isValidIP(ip string) bool {
	_, err := netip.ParseAddr(strings.TrimSpace(ip))
	return err == nil
}

// UserLogin authenticates a user (admin or regular) from the users table.
// Falls back to config-based credentials for backward compatibility.
// Returns the role on success, empty string on failure.
func (a *Authenticator) UserLogin(w http.ResponseWriter, r *http.Request, user, pass string) (string, error) {
	ip := clientIP(r)
	if ip != "" {
		banned, err := a.Catalog.IsLoginIPBanned(r.Context(), ip)
		if err != nil {
			return "", err
		}
		if banned {
			return "", ErrLoginIPBanned
		}
	}

	u, err := a.Catalog.GetUserByUsername(r.Context(), user)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		expectedUser, expectedPass := a.Credentials()
		userCount, countErr := a.Catalog.CountUsers(r.Context())
		if countErr != nil {
			return "", countErr
		}
		if userCount == 0 && expectedUser != "" && expectedPass != "" &&
			subtle.ConstantTimeCompare([]byte(user), []byte(expectedUser)) == 1 &&
			subtle.ConstantTimeCompare([]byte(pass), []byte(expectedPass)) == 1 {
			if ip != "" {
				a.clearFailures(ip)
			}
			token, err := randomToken()
			if err != nil {
				return "", err
			}
			expiresAt := a.now().Add(sessionTTL)
			if err := a.Catalog.CreateSessionUntil(r.Context(), token, expiresAt, 0); err != nil {
				return "", err
			}
			setSessionCookie(w, token, expiresAt)
			return "admin", nil
		}
		if ip != "" {
			if err := a.recordFailure(r, ip); err != nil {
				return "", err
			}
		}
		return "", nil
	}

	if u.Banned {
		return "", ErrUserBanned
	}

	if !checkPassword(pass, u.Password) {
		if ip != "" {
			if err := a.recordFailure(r, ip); err != nil {
				return "", err
			}
		}
		return "", nil
	}

	if ip != "" {
		a.clearFailures(ip)
	}

	token, err := randomToken()
	if err != nil {
		return "", err
	}
	expiresAt := a.now().Add(sessionTTL)
	if err := a.Catalog.CreateSessionUntil(r.Context(), token, expiresAt, u.ID); err != nil {
		return "", err
	}

	setSessionCookie(w, token, expiresAt)
	return u.Role, nil
}

// AdminRequired is like Required but additionally checks that the session
// belongs to a user with role="admin". Regular users get 403.
func (a *Authenticator) AdminRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, userID, err := a.ValidateRequest(w, r)
		if err != nil || !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if userID > 0 {
			u, err := a.Catalog.GetUserByID(r.Context(), userID)
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if u.Banned || u.Role != "admin" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, withSessionIdentity(r))
	})
}

func setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func checkPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
