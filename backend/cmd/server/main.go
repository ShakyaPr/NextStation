package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"stationfinder/internal/api"
	"stationfinder/internal/auth"
	"stationfinder/internal/config"
	"stationfinder/internal/data"
	"stationfinder/internal/google"
	"stationfinder/internal/locations"
	"stationfinder/internal/search"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			healthcheck()
			return
		}
	}
	defaultCfg := "../config.yaml"
	if env := os.Getenv("CONFIG_FILE"); env != "" {
		defaultCfg = env
	}
	cfgPath := flag.String("config", defaultCfg, "path to config file (or CONFIG_FILE env)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	lists, err := data.LoadConfigured(cfg.Data.ListsFile, cfg.Data.VacanciesFile)
	if err != nil {
		log.Fatal(err)
	}
	stations := data.AllStations(lists)
	au, err := auth.New(cfg.Auth)
	if err != nil {
		log.Fatal(err)
	}
	if au != nil {
		go au.Check(context.Background())
	} else {
		log.Printf("sign-in is off (no OIDC_ISSUER): anyone who can reach the app can use it")
	}
	client := google.New(cfg.Google.APIKey)
	store, err := locations.NewStoreWithSeeds(locations.Paths{
		Cache:         cfg.Data.GeocacheFile,
		Overrides:     cfg.Data.OverridesFile,
		CacheSeed:     cfg.Data.GeocacheSeedFile,
		OverridesSeed: cfg.Data.OverridesSeedFile,
	}, client, cfg.Google.UsePlacesSearch, cfg.Search.SuspectDistanceKm)
	if err != nil {
		log.Fatal(err)
	}
	for _, l := range lists {
		log.Printf("list %q: %d stations (%s)", l.ID, len(l.Stations), l.Name)
	}
	log.Printf("%d distinct stations across %d list(s)", len(stations), len(lists))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !client.Enabled() {
		log.Printf("WARNING: google.apiKey is empty – geocoding and search are disabled")
	} else if cfg.Geocoding.OnStartup {
		// Also retry stations cached as "not found": the town and district fallbacks
		// usually place them now. Each is looked up once; afterwards it has a location.
		if err := store.Run(ctx, stations, cfg.Geocoding.Workers, true); err != nil {
			log.Printf("geocoding: %v", err)
		}
	}

	svc := search.NewService(cfg, lists, store, client)
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           api.New(ctx, cfg, lists, store, svc, client, au).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("listening on http://localhost:%d", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// healthcheck exits 0 if the server answers /healthz (the image has no curl or shell).
func healthcheck() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	res, err := c.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "status", res.Status)
		os.Exit(1)
	}
}
