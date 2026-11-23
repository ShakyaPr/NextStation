// Package config loads the YAML configuration file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Google    GoogleConfig    `yaml:"google"`
	Data      DataConfig      `yaml:"data"`
	Search    SearchConfig    `yaml:"search"`
	Geocoding GeocodingConfig `yaml:"geocoding"`
	Auth      AuthConfig      `yaml:"auth"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
	// Optional: directory containing the built frontend (frontend/dist). When set and
	// present, the backend serves the UI as well, so only one process is needed.
	StaticDir string `yaml:"staticDir"`
}

type GoogleConfig struct {
	APIKey string `yaml:"apiKey"`
	// Use Places API (New) Text Search to locate stations. Falls back to the
	// Geocoding API when disabled or when Places returns nothing.
	UsePlacesSearch bool `yaml:"usePlacesSearch"`
	// Routes API travel mode: DRIVE (default) or TWO_WHEELER.
	TravelMode string `yaml:"travelMode"`
}

type DataConfig struct {
	// ListsFile is the manifest of vacancy / station lists (data/lists.json). When it is
	// empty or missing, VacanciesFile is loaded as the only list.
	ListsFile     string `yaml:"listsFile"`
	VacanciesFile string `yaml:"vacanciesFile"`
	GeocacheFile  string `yaml:"geocacheFile"`
	OverridesFile string `yaml:"overridesFile"`
	// Optional read-only copies used when GeocacheFile / OverridesFile do not exist yet.
	// In containers the writable files live in /tmp and these point at the files baked
	// into the image, so a fresh container starts with every station already located.
	GeocacheSeedFile  string `yaml:"geocacheSeedFile"`
	OverridesSeedFile string `yaml:"overridesSeedFile"`
}

type SearchConfig struct {
	DefaultLimit int `yaml:"defaultLimit"`
	// Max number of stations (closest by straight line) sent to the Routes API per search.
	MaxRouteCandidates int `yaml:"maxRouteCandidates"`
	// Upper bound on average road speed, used to pre-filter by straight-line distance
	// when only a time limit is given.
	MaxSpeedKmh float64 `yaml:"maxSpeedKmh"`
	// Stations further than this from their district centre are flagged as suspect.
	SuspectDistanceKm float64 `yaml:"suspectDistanceKm"`
}

type GeocodingConfig struct {
	OnStartup bool `yaml:"onStartup"`
	Workers   int  `yaml:"workers"`
}

// AuthConfig turns on sign-in with Keycloak (or another OpenID Connect provider) when
// the app is self-hosted, e.g. Docker on a VPS. (On Choreo, managed authentication
// does this instead and these settings stay empty.)
type AuthConfig struct {
	// Issuer is the Keycloak realm URL, e.g. https://keycloak.example.com/realms/station-finder.
	// Empty = sign-in off and the API is open. Env: OIDC_ISSUER.
	Issuer string `yaml:"issuer"`
	// The Keycloak client (confidential, standard flow). Env: OIDC_CLIENT_ID, OIDC_CLIENT_SECRET.
	ClientID     string `yaml:"clientId"`
	ClientSecret string `yaml:"clientSecret"`
	// PublicURL is the address users open, e.g. https://stations.example.com. Keycloak
	// sends users back to PublicURL/auth/callback. Empty = taken from each request.
	// Env: PUBLIC_URL.
	PublicURL string `yaml:"publicUrl"`
	// Users with this role (a client role of ClientID, a realm role or a group name)
	// may change station locations and run geocoding. Env: OIDC_ADMIN_ROLE.
	AdminRole string `yaml:"adminRole"`
	// Optional: only users with this role may sign in (useful when the realm also has
	// users of other apps). Empty = every user of the realm. Env: OIDC_REQUIRED_ROLE.
	RequiredRole string `yaml:"requiredRole"`
	// How long a sign-in lasts in this app. Removing a user in Keycloak takes effect
	// when it ends. Signing in again is automatic while the Keycloak session is alive.
	SessionHours int `yaml:"sessionHours"`
	// SessionSecret signs the session cookie. Env: SESSION_SECRET. When empty, a random
	// key is created in SessionKeyFile so sessions survive restarts.
	SessionSecret  string `yaml:"sessionSecret"`
	SessionKeyFile string `yaml:"sessionKeyFile"`
}

// Load reads the config file. Relative data paths are resolved against the
// directory containing the config file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{
		Server: ServerConfig{Port: 8080},
		Google: GoogleConfig{UsePlacesSearch: true, TravelMode: "DRIVE"},
		Data: DataConfig{
			ListsFile:     "data/lists.json",
			VacanciesFile: "data/vacancies.json",
			GeocacheFile:  "data/geocache.json",
			OverridesFile: "data/overrides.json",
		},
		Search: SearchConfig{
			DefaultLimit:       10,
			MaxRouteCandidates: 60,
			MaxSpeedKmh:        80,
			SuspectDistanceKm:  75,
		},
		Geocoding: GeocodingConfig{OnStartup: true, Workers: 4},
		Auth:      AuthConfig{ClientID: "station-finder", AdminRole: "admin", SessionHours: 12},
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if env := strings.TrimSpace(os.Getenv("GOOGLE_MAPS_API_KEY")); env != "" {
		cfg.Google.APIKey = env
	}
	cfg.Google.APIKey = strings.TrimSpace(cfg.Google.APIKey)
	for env, field := range map[string]*string{
		"OIDC_ISSUER":        &cfg.Auth.Issuer,
		"OIDC_CLIENT_ID":     &cfg.Auth.ClientID,
		"OIDC_CLIENT_SECRET": &cfg.Auth.ClientSecret,
		"OIDC_ADMIN_ROLE":    &cfg.Auth.AdminRole,
		"OIDC_REQUIRED_ROLE": &cfg.Auth.RequiredRole,
		"PUBLIC_URL":         &cfg.Auth.PublicURL,
		"SESSION_SECRET":     &cfg.Auth.SessionSecret,
	} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			*field = v
		}
	}
	cfg.Auth.Issuer = strings.TrimRight(strings.TrimSpace(cfg.Auth.Issuer), "/")
	if env := os.Getenv("PORT"); env != "" {
		port, err := strconv.Atoi(env)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT %q", env)
		}
		cfg.Server.Port = port
	}
	base := filepath.Dir(path)
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	cfg.Data.ListsFile = resolve(cfg.Data.ListsFile)
	cfg.Data.VacanciesFile = resolve(cfg.Data.VacanciesFile)
	cfg.Data.GeocacheFile = resolve(cfg.Data.GeocacheFile)
	cfg.Data.OverridesFile = resolve(cfg.Data.OverridesFile)
	cfg.Data.GeocacheSeedFile = resolve(cfg.Data.GeocacheSeedFile)
	cfg.Data.OverridesSeedFile = resolve(cfg.Data.OverridesSeedFile)
	cfg.Server.StaticDir = resolve(cfg.Server.StaticDir)
	cfg.Auth.SessionKeyFile = resolve(cfg.Auth.SessionKeyFile)
	// STATE_DIR moves every file the app writes into one directory (a Docker volume).
	if dir := os.Getenv("STATE_DIR"); dir != "" {
		cfg.Data.GeocacheFile = filepath.Join(dir, "geocache.json")
		cfg.Data.OverridesFile = filepath.Join(dir, "overrides.json")
		cfg.Auth.SessionKeyFile = filepath.Join(dir, "session.key")
	}
	if cfg.Auth.SessionKeyFile == "" {
		cfg.Auth.SessionKeyFile = filepath.Join(filepath.Dir(cfg.Data.GeocacheFile), "session.key")
	}
	if cfg.Auth.SessionHours <= 0 {
		cfg.Auth.SessionHours = 12
	}
	if cfg.Google.TravelMode == "" {
		cfg.Google.TravelMode = "DRIVE"
	}
	if cfg.Geocoding.Workers <= 0 {
		cfg.Geocoding.Workers = 4
	}
	return cfg, nil
}
