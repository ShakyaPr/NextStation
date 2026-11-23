package api

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"stationfinder/internal/auth"
	"stationfinder/internal/config"
	"stationfinder/internal/data"
	"stationfinder/internal/google"
	"stationfinder/internal/locations"
	"stationfinder/internal/search"
)

// fakeKeycloak is a minimal OpenID Connect provider shaped like Keycloak: roles are in
// the access token (realm_access / resource_access), not in the ID token.
type fakeKeycloak struct {
	*httptest.Server
	key      *rsa.PrivateKey
	clientID string
	secret   string

	mu    sync.Mutex
	codes map[string]grant
	// user signs in next (set by the test before starting the flow)
	user fakeUser
}

type fakeUser struct {
	username    string
	name        string
	clientRoles []string
	realmRoles  []string
}

type grant struct {
	user      fakeUser
	nonce     string
	challenge string
	redirect  string
}

func newFakeKeycloak(t *testing.T) *fakeKeycloak {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kc := &fakeKeycloak{key: key, clientID: "station-finder", secret: "s3cret", codes: map[string]grant{}}
	mux := http.NewServeMux()
	kc.Server = httptest.NewServer(mux)
	t.Cleanup(kc.Close)
	iss := kc.URL + "/realms/test"

	mux.HandleFunc("GET /realms/test/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                iss,
			"authorization_endpoint":                iss + "/protocol/openid-connect/auth",
			"token_endpoint":                        iss + "/protocol/openid-connect/token",
			"jwks_uri":                              iss + "/protocol/openid-connect/certs",
			"end_session_endpoint":                  iss + "/protocol/openid-connect/logout",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /realms/test/protocol/openid-connect/certs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	// The login page: "signs in" kc.user and redirects back with a code.
	mux.HandleFunc("GET /realms/test/protocol/openid-connect/auth", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != kc.clientID || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" {
			http.Error(w, "bad authorization request: "+r.URL.RawQuery, 400)
			return
		}
		code := fmt.Sprintf("code-%d", time.Now().UnixNano())
		kc.mu.Lock()
		kc.codes[code] = grant{user: kc.user, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
		kc.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
	})
	mux.HandleFunc("POST /realms/test/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		r.ParseForm()
		id, secret, ok := r.BasicAuth()
		if !ok {
			id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
		}
		kc.mu.Lock()
		g, found := kc.codes[r.Form.Get("code")]
		delete(kc.codes, r.Form.Get("code"))
		kc.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		switch {
		case id != kc.clientID || secret != kc.secret:
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"unauthorized_client"}`)
			return
		case !found || g.redirect != r.Form.Get("redirect_uri") || b64(sum[:]) != g.challenge:
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		now := time.Now().Unix()
		base := map[string]any{"iss": iss, "sub": "id-" + g.user.username, "iat": now, "exp": now + 300,
			"preferred_username": g.user.username, "name": g.user.name, "email": g.user.username + "@example.com"}
		idt := map[string]any{"aud": kc.clientID, "nonce": g.nonce, "typ": "ID"}
		at := map[string]any{"aud": "account", "typ": "Bearer",
			"realm_access":    map[string]any{"roles": append([]string{"offline_access"}, g.user.realmRoles...)},
			"resource_access": map[string]any{kc.clientID: map[string]any{"roles": g.user.clientRoles}}}
		for k, v := range base {
			idt[k], at[k] = v, v
		}
		json.NewEncoder(w).Encode(map[string]any{"token_type": "Bearer", "expires_in": 300,
			"id_token": kc.sign(idt), "access_token": kc.sign(at)})
	})
	return kc
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (kc *fakeKeycloak) sign(claims map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	p, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(p)
	sum := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, kc.key, crypto.SHA256, sum[:])
	return in + "." + b64(sig)
}

// newOIDCApp serves two lists and a built UI with Keycloak sign-in on.
func newOIDCApp(t *testing.T, kc *fakeKeycloak, requiredRole string) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "static"), 0o755)
	os.WriteFile(filepath.Join(dir, "static", "index.html"), []byte("<html>app</html>"), 0o644)

	app := httptest.NewUnstartedServer(nil)
	cfg := &config.Config{}
	cfg.Server.StaticDir = filepath.Join(dir, "static")
	cfg.Auth = config.AuthConfig{Issuer: kc.URL + "/realms/test", ClientID: kc.clientID, ClientSecret: kc.secret,
		PublicURL: "http://" + app.Listener.Addr().String(), AdminRole: "admin", RequiredRole: requiredRole,
		SessionHours: 1, SessionKeyFile: filepath.Join(dir, "state", "session.key")}
	au, err := auth.New(cfg.Auth)
	if err != nil {
		t.Fatal(err)
	}
	a := &data.Station{ID: "kegalle-a-dh", Name: "A DH", District: "KEGALLE", Province: "SABARAGAMUWA PROVINCE",
		Vacancies: []data.Vacancy{{Designation: "MO", Type: "New"}, {Designation: "SHO", Type: ""}}}
	b := &data.Station{ID: "kegalle-b-pmcu", Name: "B PMCU", District: "KEGALLE", Province: "SABARAGAMUWA PROVINCE",
		Vacancies: []data.Vacancy{{Type: "Difficult station", Designation: "MO RELIEF"}}}
	lists := []*data.List{
		data.NewList(data.ListInfo{ID: "main", Name: "Main"}, []*data.Station{a}),
		data.NewList(data.ListInfo{ID: "hard", Name: "Difficult", Kind: "stations"}, []*data.Station{b}),
	}
	gc := google.New("")
	store, err := locations.NewStore(filepath.Join(dir, "c.json"), filepath.Join(dir, "o.json"), gc, true, 75)
	if err != nil {
		t.Fatal(err)
	}
	svc := search.NewService(cfg, lists, store, gc)
	app.Config.Handler = New(context.Background(), cfg, lists, store, svc, gc, au).Handler()
	app.Start()
	t.Cleanup(app.Close)
	return app
}

type browser struct {
	t    *testing.T
	c    *http.Client
	base string
}

func newBrowser(t *testing.T, base string) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, base: base, c: &http.Client{Jar: jar}}
}

// get follows redirects (like a browser) and returns the final URL, status and body.
func (b *browser) get(path string) (string, int, string) {
	b.t.Helper()
	res, err := b.c.Get(b.base + path)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.Request.URL.String(), res.StatusCode, string(body)
}

func (b *browser) do(method, path, body string) (int, string) {
	b.t.Helper()
	req, _ := http.NewRequest(method, b.base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(out)
}

func (b *browser) signIn(kc *fakeKeycloak, u fakeUser) (finalURL string) {
	b.t.Helper()
	kc.mu.Lock()
	kc.user = u
	kc.mu.Unlock()
	final, code, _ := b.get("/auth/login")
	if code != 200 {
		b.t.Fatalf("sign-in flow ended with %d at %s", code, final)
	}
	return final
}

func TestKeycloakSignIn(t *testing.T) {
	kc := newFakeKeycloak(t)
	app := newOIDCApp(t, kc, "")
	br := newBrowser(t, app.URL)

	// The UI shell and its runtime config are public; the API is not.
	if _, code, body := br.get("/config.js"); code != 200 || !strings.Contains(body, `"authMode":"oidc"`) || !strings.Contains(body, `"managedAuth":true`) {
		t.Errorf("config.js = %d %q", code, body)
	}
	if code, _ := br.do("GET", "/api/meta", ""); code != 401 {
		t.Fatalf("meta without session: %d, want 401", code)
	}
	if code, _ := br.do("GET", "/auth/userinfo", ""); code != 401 {
		t.Errorf("userinfo without session: %d", code)
	}

	// A normal user signs in through "Keycloak" and lands back on the app.
	if final := br.signIn(kc, fakeUser{username: "doctor1", name: "Nimal Perera"}); final != app.URL+"/" {
		t.Fatalf("landed on %s, want the app", final)
	}
	code, body := br.do("GET", "/auth/userinfo", "")
	var u auth.User
	json.Unmarshal([]byte(body), &u)
	if code != 200 || u.Username != "doctor1" || u.Name != "Nimal Perera" || u.Email != "doctor1@example.com" || u.Admin {
		t.Fatalf("userinfo = %d %s", code, body)
	}
	for _, tok := range []string{"eyJ", "access_token", "id_token"} {
		if strings.Contains(body, tok) {
			t.Errorf("userinfo leaks a token: %s", body)
		}
	}
	if code, body := br.do("GET", "/api/meta?list=hard", ""); code != 200 || !strings.Contains(body, `"list":"hard"`) {
		t.Errorf("meta?list=hard: %d %s", code, body)
	}
	loc := `{"lat":7.25,"lng":80.35}`
	if code, _ := br.do("PUT", "/api/stations/kegalle-b-pmcu/location", loc); code != 403 {
		t.Errorf("non-admin PUT location: %d, want 403", code)
	}

	// Logout clears the session and sends the browser to Keycloak's logout with the
	// ID token, coming back to the app.
	noFollow := &http.Client{Jar: br.c.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := noFollow.Get(app.URL + "/auth/logout")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc302, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != 302 || !strings.HasSuffix(loc302.Path, "/protocol/openid-connect/logout") ||
		loc302.Query().Get("post_logout_redirect_uri") != app.URL+"/" || loc302.Query().Get("client_id") != "station-finder" ||
		strings.Count(loc302.Query().Get("id_token_hint"), ".") != 2 {
		t.Errorf("logout redirect = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if code, _ := br.do("GET", "/api/meta", ""); code != 401 {
		t.Errorf("meta after logout: %d, want 401", code)
	}

	// An admin (client role "admin" on the station-finder client) may fix locations.
	br.signIn(kc, fakeUser{username: "boss", clientRoles: []string{"admin"}})
	if code, body := br.do("GET", "/auth/userinfo", ""); !strings.Contains(body, `"admin":true`) {
		t.Errorf("admin userinfo = %d %s", code, body)
	}
	if code, body := br.do("PUT", "/api/stations/kegalle-b-pmcu/location", loc); code != 200 {
		t.Errorf("admin PUT location: %d %s", code, body)
	}

	// A tampered session cookie is rejected.
	appURL, _ := url.Parse(app.URL)
	for _, c := range br.c.Jar.Cookies(appURL) {
		if c.Name == "sf_session" {
			c.Value = strings.Replace(c.Value, ".", "x.", 1)
			br.c.Jar.SetCookies(appURL, []*http.Cookie{c})
		}
	}
	if code, _ := br.do("GET", "/api/meta", ""); code != 401 {
		t.Errorf("tampered cookie: %d, want 401", code)
	}
}

func TestKeycloakRealmRoleAndRequiredRole(t *testing.T) {
	kc := newFakeKeycloak(t)
	app := newOIDCApp(t, kc, "station-user")
	br := newBrowser(t, app.URL)

	// Without the required role, sign-in is refused with a message for the sign-in page.
	final := br.signIn(kc, fakeUser{username: "outsider"})
	if u, _ := url.Parse(final); u.Query().Get("code") != "access_denied" || u.Query().Get("message") == "" {
		t.Errorf("refused sign-in landed on %s", final)
	}
	if code, _ := br.do("GET", "/api/meta", ""); code != 401 {
		t.Errorf("refused user can call the API: %d", code)
	}
	// Realm roles count too.
	br.signIn(kc, fakeUser{username: "member", realmRoles: []string{"station-user", "admin"}})
	if code, body := br.do("GET", "/auth/userinfo", ""); code != 200 || !strings.Contains(body, `"admin":true`) {
		t.Errorf("member userinfo = %d %s", code, body)
	}
}

func TestKeycloakCallbackRejectsForgedState(t *testing.T) {
	kc := newFakeKeycloak(t)
	app := newOIDCApp(t, kc, "")
	br := newBrowser(t, app.URL)
	final, _, _ := br.get("/auth/callback?code=whatever&state=forged")
	if u, _ := url.Parse(final); u.Query().Get("code") != "invalid_state" {
		t.Errorf("forged callback landed on %s", final)
	}
	final, _, _ = br.get("/auth/callback?error=access_denied&error_description=User+cancelled")
	if u, _ := url.Parse(final); u.Query().Get("code") != "access_denied" || u.Query().Get("message") != "User cancelled" {
		t.Errorf("cancelled sign-in landed on %s", final)
	}
	if code, _ := br.do("GET", "/api/meta", ""); code != 401 {
		t.Errorf("API open after failed callbacks: %d", code)
	}
}

func TestOpenAPIAdminTagsMatch(t *testing.T) {
	raw, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	tagged := map[string]bool{}
	for path, ops := range spec.Paths {
		for method, op := range ops {
			opMap, ok := op.(map[string]any)
			if !ok {
				continue // path-level "parameters"
			}
			tags, _ := opMap["tags"].([]any)
			for _, tag := range tags {
				if tag == "admin" {
					tagged[strings.ToUpper(method)+" "+path] = true
				}
			}
		}
	}
	for op := range adminRoutes {
		if !tagged[op] {
			t.Errorf("%s is admin-only in the server but not tagged admin in openapi.yaml", op)
		}
		delete(tagged, op)
	}
	for op := range tagged {
		t.Errorf("%s is tagged admin in openapi.yaml but open to all users in the server", op)
	}
}
