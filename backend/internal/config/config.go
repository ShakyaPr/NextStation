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

// AuthConfig turns on the built-in sign-in (used when the app is self-hosted, e.g. on
// a VPS with Docker; on Choreo, managed authentication does this instead).
type AuthConfig struct {
	// UsersFile is a CSV of the people who may sign in:
	//   username,password,groups,first_name,last_name,email
	// password is a bcrypt hash (stationfinder hash-password) or, discouraged, plain text.
	// Empty = sign-in off and the API is open. Env: AUTH_USERS_FILE.
	UsersFile string `yaml:"usersFile"`
	// Members of this group may change station locations and run geocoding.
	AdminGroup string `yaml:"adminGroup"`
	// How long a sign-in lasts.
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
		Auth:      AuthConfig{AdminGroup: "admins", SessionHours: 24 * 7},
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if env := strings.TrimSpace(os.Getenv("GOOGLE_MAPS_API_KEY")); env != "" {
		cfg.Google.APIKey = env
	}
	cfg.Google.APIKey = strings.TrimSpace(cfg.Google.APIKey)
	if env := os.Getenv("AUTH_USERS_FILE"); env != "" {
		cfg.Auth.UsersFile = env
	}
	if env := os.Getenv("SESSION_SECRET"); env != "" {
		cfg.Auth.SessionSecret = env
	}
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
	cfg.Auth.UsersFile = resolve(cfg.Auth.UsersFile)
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
	if cfg.Auth.AdminGroup == "" {
		cfg.Auth.AdminGroup = "admins"
	}
	if cfg.Auth.SessionHours <= 0 {
		cfg.Auth.SessionHours = 24 * 7
	}
	if cfg.Google.TravelMode == "" {
		cfg.Google.TravelMode = "DRIVE"
	}
	if cfg.Geocoding.Workers <= 0 {
		cfg.Geocoding.Workers = 4
	}
	return cfg, nil
}
