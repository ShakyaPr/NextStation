// Package config loads the YAML configuration file.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Google    GoogleConfig    `yaml:"google"`
	Data      DataConfig      `yaml:"data"`
	Search    SearchConfig    `yaml:"search"`
	Geocoding GeocodingConfig `yaml:"geocoding"`
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
	VacanciesFile string `yaml:"vacanciesFile"`
	GeocacheFile  string `yaml:"geocacheFile"`
	OverridesFile string `yaml:"overridesFile"`
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
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if env := os.Getenv("GOOGLE_MAPS_API_KEY"); env != "" {
		cfg.Google.APIKey = env
	}
	base := filepath.Dir(path)
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	cfg.Data.VacanciesFile = resolve(cfg.Data.VacanciesFile)
	cfg.Data.GeocacheFile = resolve(cfg.Data.GeocacheFile)
	cfg.Data.OverridesFile = resolve(cfg.Data.OverridesFile)
	cfg.Server.StaticDir = resolve(cfg.Server.StaticDir)
	if cfg.Google.TravelMode == "" {
		cfg.Google.TravelMode = "DRIVE"
	}
	if cfg.Geocoding.Workers <= 0 {
		cfg.Geocoding.Workers = 4
	}
	return cfg, nil
}
