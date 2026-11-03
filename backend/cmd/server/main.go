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
	"stationfinder/internal/config"
	"stationfinder/internal/data"
	"stationfinder/internal/google"
	"stationfinder/internal/locations"
	"stationfinder/internal/search"
)

func main() {
	cfgPath := flag.String("config", "../config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	stations, err := data.LoadStations(cfg.Data.VacanciesFile)
	if err != nil {
		log.Fatal(err)
	}
	client := google.New(cfg.Google.APIKey)
	store, err := locations.NewStore(cfg.Data.GeocacheFile, cfg.Data.OverridesFile, client,
		cfg.Google.UsePlacesSearch, cfg.Search.SuspectDistanceKm)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("loaded %d stations from %s", len(stations), cfg.Data.VacanciesFile)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !client.Enabled() {
		log.Printf("WARNING: google.apiKey is empty – geocoding and search are disabled")
	} else if cfg.Geocoding.OnStartup {
		if err := store.Run(ctx, stations, cfg.Geocoding.Workers, false); err != nil {
			log.Printf("geocoding: %v", err)
		}
	}

	svc := search.NewService(cfg, stations, store, client)
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           api.New(ctx, cfg, stations, store, svc, client).Handler(),
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
