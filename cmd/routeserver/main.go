// Command routeserver runs the route-optimisation HTTP API.
//
//	go run ./cmd/routeserver                          # haversine only, :8080
//	go run ./cmd/routeserver -osrm http://localhost:5000
//	go run ./cmd/routeserver -addr :9090 -osrm https://router.project-osrm.org
//
// POST /optimize with a JSON body of {stops, metric, mode}; GET /healthz
// for liveness. See the api package for the request/response schema.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Kaushlendra-Kumar/routesolver/api"
	"github.com/Kaushlendra-Kumar/routesolver/geocode"
	"github.com/Kaushlendra-Kumar/routesolver/osrm"
	"github.com/Kaushlendra-Kumar/routesolver/web"
)

func main() {
	var (
		addr         = flag.String("addr", ":8080", "listen address")
		osrmURL      = flag.String("osrm", "", "OSRM base URL (empty = haversine only). e.g. http://localhost:5000")
		osrmTimeout  = flag.Duration("osrm-timeout", 15*time.Second, "per-request timeout for OSRM calls")
		nominatimURL = flag.String("nominatim", geocode.PublicServer, "Nominatim base URL for geocoding (empty to disable)")
		userAgent    = flag.String("user-agent", "routesolver-demo/1.0 (github.com/Kaushlendra-Kumar/routesolver)", "User-Agent for Nominatim (its policy requires one)")
		solveTimeout = flag.Duration("solve-timeout", 30*time.Second, "max time for a single optimise request")
		maxStops     = flag.Int("max-stops", 100, "reject requests with more stops than this")
	)
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags)

	cfg := api.Config{
		MaxStops:     *maxStops,
		SolveTimeout: *solveTimeout,
		Logger:       logger,
		UI:           web.FS, // serve the embedded frontend at GET /
	}
	if *osrmURL != "" {
		cfg.OSRM = osrm.New(*osrmURL, *osrmTimeout)
		logger.Printf("OSRM enabled: %s (road_distance / road_duration metrics available)", *osrmURL)
	} else {
		logger.Printf("OSRM not configured: only the haversine metric is available (start with -osrm to enable road metrics)")
	}
	if *nominatimURL != "" {
		cfg.Geocoder = geocode.New(*nominatimURL, *userAgent, *osrmTimeout, time.Second)
		logger.Printf("Geocoding enabled via %s (address input available)", *nominatimURL)
	} else {
		logger.Printf("Geocoding disabled: the UI accepts coordinates only")
	}

	srv := &http.Server{
		Addr:    *addr,
		Handler: api.NewServer(cfg).Handler(),
		// Timeouts guard against slow or stuck clients (e.g. Slowloris).
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      *solveTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Run the server in the background so main can wait for a signal.
	serverErr := make(chan error, 1)
	go func() {
		logger.Printf("listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Block until an interrupt/terminate signal or a fatal server error.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		logger.Fatalf("server error: %v", err)
	case sig := <-stop:
		logger.Printf("received %s, shutting down gracefully...", sig)
	}

	// Give in-flight requests up to 10s to finish before forcing exit.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatalf("graceful shutdown failed: %v", err)
	}
	logger.Printf("shutdown complete")
}
