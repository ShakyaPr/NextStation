// Package auth is the built-in sign-in used when the app is self-hosted (Docker on a
// VPS). It mirrors the endpoints of Choreo's managed authentication, so the frontend
// works the same way on both:
//
//	POST /auth/login     {"username","password"} -> 200 user + session cookie
//	GET  /auth/userinfo  200 user when signed in, 401 otherwise
//	POST /auth/logout    clears the session
//
// Users come from a CSV file; sessions are HMAC-signed cookies, so nothing is stored
// on the server.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"stationfinder/internal/config"
)

const cookieName = "sf_session"

// User is what the frontend receives from /auth/userinfo (OIDC-style claim names).
type User struct {
	Username   string   `json:"username"`
	Name       string   `json:"name,omitempty"`
	GivenName  string   `json:"given_name,omitempty"`
	FamilyName string   `json:"family_name,omitempty"`
	Email      string   `json:"email,omitempty"`
	Groups     []string `json:"groups"`
	password   string   // bcrypt hash, or plain text
}

func (u *User) InGroup(g string) bool {
	for _, x := range u.Groups {
		if strings.EqualFold(x, g) {
			return true
		}
	}
	return false
}

func (u *User) checkPassword(pw string) bool {
	if isBcrypt(u.password) {
		return bcrypt.CompareHashAndPassword([]byte(u.password), []byte(pw)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(u.password), []byte(pw)) == 1
}

func isBcrypt(s string) bool {
	return strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$")
}

// HashPassword returns a bcrypt hash for the users file.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(h), err
}

// LoadUsers reads the users CSV (header: username,password,groups,first_name,last_name,email).
// groups may be written as "[admins, users]", "admins;users" or "admins users".
func LoadUsers(path string) (map[string]*User, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")))] = i
	}
	if _, ok := col["username"]; !ok {
		return nil, fmt.Errorf("%s: header must include username and password columns", path)
	}
	if _, ok := col["password"]; !ok {
		return nil, fmt.Errorf("%s: header must include username and password columns", path)
	}
	field := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	users := map[string]*User{}
	for line := 2; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		u := &User{
			Username:   field(rec, "username"),
			password:   field(rec, "password"),
			GivenName:  field(rec, "first_name"),
			FamilyName: field(rec, "last_name"),
			Email:      field(rec, "email"),
			Groups:     parseGroups(field(rec, "groups")),
		}
		if u.Username == "" && u.password == "" {
			continue
		}
		if u.Username == "" || u.password == "" {
			return nil, fmt.Errorf("%s line %d: username and password are required", path, line)
		}
		key := strings.ToLower(u.Username)
		if _, dup := users[key]; dup {
			return nil, fmt.Errorf("%s line %d: duplicate username %q", path, line, u.Username)
		}
		u.Name = strings.TrimSpace(u.GivenName + " " + u.FamilyName)
		users[key] = u
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("%s: no users", path)
	}
	return users, nil
}

func parseGroups(s string) []string {
	s = strings.Trim(s, "[] ")
	out := []string{}
	for _, g := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		out = append(out, g)
	}
	return out
}

// Auth checks credentials and session cookies.
type Auth struct {
	users      map[string]*User
	key        []byte
	ttl        time.Duration
	adminGroup string
	limiter    *limiter
}

// New returns nil (sign-in off) when no users file is configured.
func New(cfg config.AuthConfig) (*Auth, error) {
	if cfg.UsersFile == "" {
		return nil, nil
	}
	users, err := LoadUsers(cfg.UsersFile)
	if err != nil {
		return nil, fmt.Errorf("auth users: %w", err)
	}
	plain := 0
	for _, u := range users {
		if !isBcrypt(u.password) {
			plain++
		}
	}
	if plain > 0 {
		log.Printf("WARNING: %d user(s) in %s have plain-text passwords; replace them with hashes from `stationfinder hash-password`", plain, cfg.UsersFile)
	}
	key, err := sessionKey(cfg)
	if err != nil {
		return nil, err
	}
	log.Printf("sign-in enabled for %d user(s) from %s", len(users), cfg.UsersFile)
	return &Auth{
		users:      users,
		key:        key,
		ttl:        time.Duration(cfg.SessionHours) * time.Hour,
		adminGroup: cfg.AdminGroup,
		limiter:    &limiter{max: 10, window: 15 * time.Minute, fails: map[string]*failures{}},
	}, nil
}

// sessionKey uses SESSION_SECRET, or a random key kept in SessionKeyFile so that
// sign-ins survive restarts.
func sessionKey(cfg config.AuthConfig) ([]byte, error) {
	if cfg.SessionSecret != "" {
		if len(cfg.SessionSecret) < 32 {
			return nil, errors.New("SESSION_SECRET must be at least 32 characters")
		}
		return []byte(cfg.SessionSecret), nil
	}
	if raw, err := os.ReadFile(cfg.SessionKeyFile); err == nil && len(raw) >= 32 {
		return raw, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SessionKeyFile), 0o755); err == nil {
		if err := os.WriteFile(cfg.SessionKeyFile, key, 0o600); err != nil {
			log.Printf("WARNING: cannot save session key (%v); everyone is signed out on restart", err)
		}
	}
	return key, nil
}

func (a *Auth) IsAdmin(u *User) bool { return u != nil && u.InGroup(a.adminGroup) }

// User returns the signed-in user for a request, or nil.
func (a *Auth) User(r *http.Request) *User {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil
	}
	payload, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return nil
	}
	want := a.sign(payload)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil
	}
	name, exp, ok := strings.Cut(string(raw), "|")
	if !ok {
		return nil
	}
	expiry, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > expiry {
		return nil
	}
	return a.users[name] // nil if the user was removed from the file
}

func (a *Auth) sign(payload string) string {
	m := hmac.New(sha256.New, a.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (a *Auth) setSession(w http.ResponseWriter, r *http.Request, u *User) {
	exp := time.Now().Add(a.ttl)
	payload := base64.RawURLEncoding.EncodeToString([]byte(strings.ToLower(u.Username) + "|" + strconv.FormatInt(exp.Unix(), 10)))
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: payload + "." + a.sign(payload), Path: "/", Expires: exp,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Login handles POST /auth/login.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Send a username and password."})
		return
	}
	ip := clientIP(r)
	if wait := a.limiter.blocked(ip); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": fmt.Sprintf("Too many failed sign-in attempts. Try again in %d minutes.", int(wait.Minutes())+1)})
		return
	}
	u := a.users[strings.ToLower(strings.TrimSpace(body.Username))]
	if u == nil || !u.checkPassword(body.Password) {
		if u == nil {
			// Spend about the same time as a real check so usernames can't be probed.
			_ = bcrypt.CompareHashAndPassword(dummyHash(), []byte(body.Password))
		}
		a.limiter.fail(ip)
		log.Printf("sign-in failed for %q from %s", body.Username, ip)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Wrong username or password."})
		return
	}
	a.limiter.reset(ip)
	a.setSession(w, r, u)
	log.Printf("signed in: %s", u.Username)
	writeJSON(w, http.StatusOK, u)
}

var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("not a real password"), 12)
	return h
})

// UserInfo handles GET /auth/userinfo.
func (a *Auth) UserInfo(w http.ResponseWriter, r *http.Request) {
	u := a.User(r)
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not signed in"})
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// Logout handles POST /auth/logout.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

// clientIP is the address of the caller. Behind a reverse proxy (Caddy in the Docker
// setup) it is the last X-Forwarded-For entry, which the proxy itself appended.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limiter blocks an address for a while after too many failed sign-ins.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string]*failures
}

type failures struct {
	n     int
	first time.Time
}

func (l *limiter) blocked(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[ip]
	if f == nil {
		return 0
	}
	left := l.window - time.Since(f.first)
	if left <= 0 {
		delete(l.fails, ip)
		return 0
	}
	if f.n >= l.max {
		return left
	}
	return 0
}

func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fails) > 10_000 {
		l.fails = map[string]*failures{} // crude bound on memory
	}
	f := l.fails[ip]
	if f == nil || time.Since(f.first) > l.window {
		f = &failures{first: time.Now()}
		l.fails[ip] = f
	}
	f.n++
}

func (l *limiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
