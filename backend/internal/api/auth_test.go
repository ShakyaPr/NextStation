package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"stationfinder/internal/auth"
	"stationfinder/internal/config"
	"stationfinder/internal/data"
	"stationfinder/internal/google"
	"stationfinder/internal/locations"
	"stationfinder/internal/search"
)

// newAuthServer serves two lists and a built UI, with sign-in on.
func newAuthServer(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	hash, err := auth.HashPassword("admin-secret")
	if err != nil {
		t.Fatal(err)
	}
	users := "username,password,groups,first_name,last_name,email\n" +
		`"boss","` + hash + `","[admins, users]","Station","Admin","boss@example.com"` + "\n" +
		`"doc","plain-pass","[users]","Nimal","Perera","doc@example.com"` + "\n"
	os.WriteFile(filepath.Join(dir, "users.csv"), []byte(users), 0o600)
	os.MkdirAll(filepath.Join(dir, "static"), 0o755)
	os.WriteFile(filepath.Join(dir, "static", "index.html"), []byte("<html>app</html>"), 0o644)

	cfg := &config.Config{}
	cfg.Server.StaticDir = filepath.Join(dir, "static")
	cfg.Auth = config.AuthConfig{UsersFile: filepath.Join(dir, "users.csv"), AdminGroup: "admins", SessionHours: 1,
		SessionKeyFile: filepath.Join(dir, "state", "session.key")}
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
	client := google.New("")
	store, err := locations.NewStore(filepath.Join(dir, "c.json"), filepath.Join(dir, "o.json"), client, true, 75)
	if err != nil {
		t.Fatal(err)
	}
	svc := search.NewService(cfg, lists, store, client)
	return New(context.Background(), cfg, lists, store, svc, client, au).Handler()
}

type client struct {
	t      *testing.T
	h      http.Handler
	cookie string
}

func (c *client) do(method, path, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "sf_session" {
			c.cookie = ck.Name + "=" + ck.Value
			if ck.MaxAge < 0 {
				c.cookie = ""
			}
		}
	}
	return rec
}

func TestBuiltInSignIn(t *testing.T) {
	h := newAuthServer(t)
	c := &client{t: t, h: h}

	// The UI shell and its runtime config are public; the API is not.
	if rec := c.do("GET", "/", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("index: %d %q", rec.Code, rec.Body.String())
	}
	if rec := c.do("GET", "/config.js", ""); !strings.Contains(rec.Body.String(), `"managedAuth":true`) ||
		!strings.Contains(rec.Body.String(), `"authMode":"local"`) {
		t.Errorf("config.js = %q", rec.Body.String())
	}
	if rec := c.do("GET", "/healthz", ""); rec.Code != 200 {
		t.Errorf("healthz: %d", rec.Code)
	}
	if rec := c.do("GET", "/api/meta", ""); rec.Code != 401 {
		t.Fatalf("meta without session: %d, want 401", rec.Code)
	}
	if rec := c.do("GET", "/auth/userinfo", ""); rec.Code != 401 {
		t.Errorf("userinfo without session: %d", rec.Code)
	}

	// Wrong password, then a plain-text user signs in (username is case-insensitive).
	if rec := c.do("POST", "/auth/login", `{"username":"doc","password":"nope"}`); rec.Code != 401 || c.cookie != "" {
		t.Fatalf("bad login: %d cookie=%q", rec.Code, c.cookie)
	}
	rec := c.do("POST", "/auth/login", `{"username":"DOC","password":"plain-pass"}`)
	if rec.Code != 200 || c.cookie == "" {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	var u auth.User
	json.Unmarshal(rec.Body.Bytes(), &u)
	if u.Name != "Nimal Perera" || len(u.Groups) != 1 || u.Groups[0] != "users" || strings.Contains(rec.Body.String(), "plain-pass") {
		t.Errorf("user = %s", rec.Body.String())
	}
	if rec := c.do("GET", "/auth/userinfo", ""); rec.Code != 200 {
		t.Errorf("userinfo: %d", rec.Code)
	}

	// Lists: the default and a named one; unknown is a 400.
	var meta struct {
		List            string        `json:"list"`
		Lists           []listSummary `json:"lists"`
		StationCount    int           `json:"stationCount"`
		HasDesignations bool          `json:"hasDesignations"`
		Types           []countItem   `json:"types"`
	}
	rec = c.do("GET", "/api/meta", "")
	json.Unmarshal(rec.Body.Bytes(), &meta)
	if rec.Code != 200 || meta.List != "main" || len(meta.Lists) != 2 || !meta.HasDesignations || len(meta.Types) != 1 {
		t.Fatalf("meta: %d %s", rec.Code, rec.Body.String())
	}
	rec = c.do("GET", "/api/meta?list=hard", "")
	json.Unmarshal(rec.Body.Bytes(), &meta)
	if meta.List != "hard" || meta.HasDesignations || meta.StationCount != 1 {
		t.Errorf("meta?list=hard: %s", rec.Body.String())
	}
	if rec := c.do("GET", "/api/meta?list=nope", ""); rec.Code != 400 {
		t.Errorf("unknown list: %d", rec.Code)
	}
	var sts []map[string]any
	json.Unmarshal(c.do("GET", "/api/stations?list=hard", "").Body.Bytes(), &sts)
	if len(sts) != 1 || sts[0]["id"] != "kegalle-b-pmcu" {
		t.Errorf("stations?list=hard = %v", sts)
	}

	// A normal user can't change locations; an admin can, for a station in any list.
	loc := `{"lat":7.25,"lng":80.35}`
	if rec := c.do("PUT", "/api/stations/kegalle-b-pmcu/location", loc); rec.Code != 403 {
		t.Errorf("user PUT location: %d, want 403", rec.Code)
	}
	if rec := c.do("POST", "/auth/logout", ""); rec.Code != 204 || c.cookie != "" {
		t.Errorf("logout: %d cookie=%q", rec.Code, c.cookie)
	}
	if rec := c.do("GET", "/api/meta", ""); rec.Code != 401 {
		t.Errorf("meta after logout: %d", rec.Code)
	}
	if rec := c.do("POST", "/auth/login", `{"username":"boss","password":"admin-secret"}`); rec.Code != 200 {
		t.Fatalf("admin login: %d", rec.Code)
	}
	if rec := c.do("PUT", "/api/stations/kegalle-b-pmcu/location", loc); rec.Code != 200 {
		t.Errorf("admin PUT location: %d %s", rec.Code, rec.Body.String())
	}

	// A tampered cookie is rejected.
	c.cookie = c.cookie[:len(c.cookie)-2] + "xx"
	if rec := c.do("GET", "/api/meta", ""); rec.Code != 401 {
		t.Errorf("tampered cookie: %d", rec.Code)
	}
}

func TestSignInRateLimit(t *testing.T) {
	c := &client{t: t, h: newAuthServer(t)}
	for i := 0; i < 10; i++ {
		c.do("POST", "/auth/login", `{"username":"doc","password":"wrong"}`)
	}
	if rec := c.do("POST", "/auth/login", `{"username":"doc","password":"plain-pass"}`); rec.Code != 429 {
		t.Errorf("after 10 failures: %d, want 429", rec.Code)
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
