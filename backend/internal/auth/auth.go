// Package auth signs users in with Keycloak (or any OpenID Connect provider) when the
// app is self-hosted (Docker on a VPS / Dokploy).
//
// The Go server is the OIDC client: a confidential client using the authorization code
// flow with PKCE. The browser only ever holds an HttpOnly, HMAC-signed session cookie,
// never a token. The endpoints mirror Choreo's managed authentication, so the frontend
// behaves the same on both:
//
//	GET /auth/login      redirect to the Keycloak sign-in page
//	GET /auth/callback   Keycloak redirects back here; starts the session
//	GET /auth/userinfo   200 + user when signed in, 401 otherwise
//	GET /auth/logout     ends the session here and in Keycloak
//
// Who may sign in is managed in Keycloak (users, and optionally a required role); users
// with the admin role may change station locations.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"stationfinder/internal/config"
)

const (
	sessionCookie = "sf_session" // the signed-in user
	idTokenCookie = "sf_idt"     // Keycloak ID token, only sent back to Keycloak at logout
	flowCookie    = "sf_oidc"    // state, nonce and PKCE verifier while signing in
	flowTTL       = 10 * time.Minute
	callbackPath  = "/auth/callback"
)

// User is what the frontend receives from /auth/userinfo (OIDC claim names).
type User struct {
	Subject    string `json:"sub"`
	Username   string `json:"username"`
	Name       string `json:"name,omitempty"`
	GivenName  string `json:"given_name,omitempty"`
	FamilyName string `json:"family_name,omitempty"`
	Email      string `json:"email,omitempty"`
	Admin      bool   `json:"admin"`
	Expires    int64  `json:"exp"`
}

// Auth runs the sign-in flow and checks session cookies.
type Auth struct {
	cfg    config.AuthConfig
	key    []byte
	ttl    time.Duration
	client *http.Client

	mu       sync.Mutex
	provider *oidc.Provider // discovered on first use, so the app starts even if Keycloak is down
}

// New returns nil (sign-in off) when no issuer is configured.
func New(cfg config.AuthConfig) (*Auth, error) {
	if cfg.Issuer == "" {
		return nil, nil
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("auth: OIDC_CLIENT_ID and OIDC_CLIENT_SECRET are required when OIDC_ISSUER is set")
	}
	if cfg.PublicURL != "" {
		u, err := url.Parse(cfg.PublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("auth: PUBLIC_URL %q must look like https://stations.example.com", cfg.PublicURL)
		}
	}
	key, err := sessionKey(cfg)
	if err != nil {
		return nil, err
	}
	a := &Auth{cfg: cfg, key: key, ttl: time.Duration(cfg.SessionHours) * time.Hour,
		client: &http.Client{Timeout: 15 * time.Second}}
	required := ""
	if cfg.RequiredRole != "" {
		required = fmt.Sprintf(", required role %q", cfg.RequiredRole)
	}
	log.Printf("sign-in with %s (client %q, admin role %q%s)", cfg.Issuer, cfg.ClientID, cfg.AdminRole, required)
	return a, nil
}

// Check contacts the identity provider once, so configuration mistakes show up in the
// startup log rather than at the first sign-in. Failure is not fatal.
func (a *Auth) Check(ctx context.Context) {
	if _, err := a.getProvider(ctx); err != nil {
		log.Printf("WARNING: sign-in server not reachable yet: %v", err)
	}
}

func (a *Auth) getProvider(ctx context.Context) (*oidc.Provider, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.provider != nil {
		return a.provider, nil
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, a.client), 15*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(ctx, a.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover %s: %w", a.cfg.Issuer, err)
	}
	a.provider = p
	return p, nil
}

// publicURL is the app's external base URL (no trailing slash).
func (a *Auth) publicURL(r *http.Request) string {
	if a.cfg.PublicURL != "" {
		return strings.TrimRight(a.cfg.PublicURL, "/")
	}
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (a *Auth) oauthConfig(r *http.Request, p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     a.cfg.ClientID,
		ClientSecret: a.cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  a.publicURL(r) + callbackPath,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
}

func (a *Auth) secure(r *http.Request) bool {
	return isHTTPS(r) || strings.HasPrefix(a.cfg.PublicURL, "https://")
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// ---------------------------------------------------------------- handlers

type flow struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Expires  int64  `json:"exp"`
}

// Login handles GET /auth/login: send the browser to Keycloak.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	p, err := a.getProvider(r.Context())
	if err != nil {
		log.Printf("sign-in: %v", err)
		a.fail(w, r, "server_error", "The sign-in server can't be reached. Try again in a minute.")
		return
	}
	f := flow{State: randomString(), Nonce: randomString(), Verifier: oauth2.GenerateVerifier(),
		Expires: time.Now().Add(flowTTL).Unix()}
	a.setCookie(w, r, flowCookie, a.seal(f), "/auth/", time.Now().Add(flowTTL))
	target := a.oauthConfig(r, p).AuthCodeURL(f.State, oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier))
	http.Redirect(w, r, target, http.StatusFound)
}

// Callback handles GET /auth/callback: Keycloak sends the browser back with a code.
func (a *Auth) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a.clearCookie(w, r, flowCookie, "/auth/")
	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = "Sign-in was cancelled or failed."
		}
		a.fail(w, r, e, msg)
		return
	}
	var f flow
	c, err := r.Cookie(flowCookie)
	if err != nil || !a.open(c.Value, &f) || time.Now().Unix() > f.Expires ||
		subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(f.State)) != 1 {
		a.fail(w, r, "invalid_state", "Sign-in took too long or was started in another tab. Please sign in again.")
		return
	}
	p, err := a.getProvider(r.Context())
	if err != nil {
		log.Printf("sign-in callback: %v", err)
		a.fail(w, r, "server_error", "The sign-in server can't be reached. Try again in a minute.")
		return
	}
	ctx := oidc.ClientContext(r.Context(), a.client)
	tok, err := a.oauthConfig(r, p).Exchange(ctx, q.Get("code"), oauth2.VerifierOption(f.Verifier))
	if err != nil {
		log.Printf("sign-in: code exchange: %v", err)
		a.fail(w, r, "token_error", "Sign-in could not be completed. Check the client secret and redirect URI settings.")
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	idt, err := p.Verifier(&oidc.Config{ClientID: a.cfg.ClientID}).Verify(ctx, rawID)
	if err != nil || idt.Nonce != f.Nonce {
		log.Printf("sign-in: invalid ID token: %v", err)
		a.fail(w, r, "token_error", "Sign-in could not be completed (invalid ID token).")
		return
	}
	var cl claims
	if err := idt.Claims(&cl); err != nil {
		a.fail(w, r, "token_error", "Sign-in could not be completed (unreadable ID token).")
		return
	}
	// Keycloak puts roles in the access token (not the ID token) by default.
	roles := cl.roles(a.cfg.ClientID)
	if at, err := p.Verifier(&oidc.Config{SkipClientIDCheck: true}).Verify(ctx, tok.AccessToken); err == nil {
		var acl claims
		if at.Claims(&acl) == nil {
			roles = append(roles, acl.roles(a.cfg.ClientID)...)
		}
	}
	username := firstNonEmpty(cl.PreferredUsername, cl.Email, cl.Subject)
	if a.cfg.RequiredRole != "" && !slices.Contains(roles, a.cfg.RequiredRole) {
		log.Printf("sign-in refused for %s: missing role %q", username, a.cfg.RequiredRole)
		a.fail(w, r, "access_denied", "Your account isn't allowed to use Station Finder. Ask an administrator for access.")
		return
	}
	u := &User{
		Subject: cl.Subject, Username: username,
		Name:      firstNonEmpty(cl.Name, strings.TrimSpace(cl.GivenName+" "+cl.FamilyName)),
		GivenName: cl.GivenName, FamilyName: cl.FamilyName, Email: cl.Email,
		Admin:   a.cfg.AdminRole != "" && slices.Contains(roles, a.cfg.AdminRole),
		Expires: time.Now().Add(a.ttl).Unix(),
	}
	exp := time.Unix(u.Expires, 0)
	a.setCookie(w, r, sessionCookie, a.seal(u), "/", exp)
	// Kept only to end the Keycloak session at logout without a confirmation page.
	if len(rawID) < 3800 {
		a.setCookie(w, r, idTokenCookie, rawID, "/auth/", exp)
	}
	log.Printf("signed in: %s (admin=%v)", u.Username, u.Admin)
	http.Redirect(w, r, "/", http.StatusFound)
}

// UserInfo handles GET /auth/userinfo.
func (a *Auth) UserInfo(w http.ResponseWriter, r *http.Request) {
	u := a.User(r)
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not signed in"})
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// Logout handles GET /auth/logout: clear the session, then end the Keycloak session
// and come back to the app.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	var idToken string
	if c, err := r.Cookie(idTokenCookie); err == nil {
		idToken = c.Value
	}
	a.clearCookie(w, r, sessionCookie, "/")
	a.clearCookie(w, r, idTokenCookie, "/auth/")
	back := a.publicURL(r) + "/"
	target := back
	if p, err := a.getProvider(r.Context()); err == nil {
		var meta struct {
			EndSession string `json:"end_session_endpoint"`
		}
		if p.Claims(&meta) == nil && meta.EndSession != "" {
			if u, err := url.Parse(meta.EndSession); err == nil {
				q := u.Query()
				q.Set("client_id", a.cfg.ClientID)
				q.Set("post_logout_redirect_uri", back)
				if idToken != "" {
					q.Set("id_token_hint", idToken)
				}
				u.RawQuery = q.Encode()
				target = u.String()
			}
		}
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// fail returns to the app, which shows ?code=&message= on its sign-in page.
func (a *Auth) fail(w http.ResponseWriter, r *http.Request, code, msg string) {
	http.Redirect(w, r, "/?"+url.Values{"code": {code}, "message": {msg}}.Encode(), http.StatusFound)
}

// User returns the signed-in user for a request, or nil.
func (a *Auth) User(r *http.Request) *User {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	var u User
	if !a.open(c.Value, &u) || time.Now().Unix() > u.Expires {
		return nil
	}
	return &u
}

func (a *Auth) IsAdmin(u *User) bool { return u != nil && u.Admin }

// ---------------------------------------------------------------- claims

type access struct {
	Roles []string `json:"roles"`
}

// claims are the parts of Keycloak's tokens we use.
type claims struct {
	Subject           string            `json:"sub"`
	PreferredUsername string            `json:"preferred_username"`
	Name              string            `json:"name"`
	GivenName         string            `json:"given_name"`
	FamilyName        string            `json:"family_name"`
	Email             string            `json:"email"`
	RealmAccess       access            `json:"realm_access"`    // realm roles
	ResourceAccess    map[string]access `json:"resource_access"` // client roles, per client
	Groups            []string          `json:"groups"`          // only with a group mapper
	Roles             []string          `json:"roles"`           // only with a custom mapper
}

// roles merges this client's roles, realm roles, groups (without the leading "/") and
// any flat "roles" claim, so a role can be granted in whichever way suits the realm.
func (c claims) roles(clientID string) []string {
	out := append([]string{}, c.ResourceAccess[clientID].Roles...)
	out = append(out, c.RealmAccess.Roles...)
	out = append(out, c.Roles...)
	for _, g := range c.Groups {
		out = append(out, strings.TrimPrefix(g, "/"))
	}
	return out
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// ---------------------------------------------------------------- cookies

// seal encodes v as base64(JSON) plus an HMAC signature; open verifies and decodes it.
func (a *Auth) seal(v any) string {
	raw, _ := json.Marshal(v)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + a.sign(payload)
}

func (a *Auth) open(value string, v any) bool {
	payload, sig, ok := strings.Cut(value, ".")
	if !ok || subtle.ConstantTimeCompare([]byte(sig), []byte(a.sign(payload))) != 1 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	return err == nil && json.Unmarshal(raw, v) == nil
}

func (a *Auth) sign(payload string) string {
	m := hmac.New(sha256.New, a.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (a *Auth) setCookie(w http.ResponseWriter, r *http.Request, name, value, path string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, Expires: exp,
		HttpOnly: true, Secure: a.secure(r), SameSite: http.SameSiteLaxMode})
}

func (a *Auth) clearCookie(w http.ResponseWriter, r *http.Request, name, path string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1,
		HttpOnly: true, Secure: a.secure(r), SameSite: http.SameSiteLaxMode})
}

func randomString() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
