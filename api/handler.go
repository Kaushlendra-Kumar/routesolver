// Package api exposes the route solver over HTTP.
//
// POST /optimize accepts a depot plus stops as JSON and returns the
// optimised visiting order. Distances come from one of three metrics:
// local haversine (default, no network), OSRM road distance, or OSRM road
// duration. The solver auto-selects exact vs heuristic by problem size.
//
// GET /healthz is a liveness probe.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/Kaushlendra-Kumar/routesolver/geocode"
	"github.com/Kaushlendra-Kumar/routesolver/osrm"
	"github.com/Kaushlendra-Kumar/routesolver/solver"
)

var errNoOSRM = errors.New("road metrics require an OSRM server; start the service with -osrm")

// Server holds handler dependencies and configuration.
type Server struct {
	osrm           *osrm.Client    // nil unless configured
	geocoder       *geocode.Client // nil unless configured
	ui             fs.FS           // nil unless a frontend is embedded
	maxStops       int
	solveTimeout   time.Duration
	geocodeTimeout time.Duration
	logger         *log.Logger
}

// Config configures NewServer. Zero fields fall back to sensible defaults.
type Config struct {
	// OSRM enables the road_distance / road_duration metrics. When nil,
	// only the haversine metric is available.
	OSRM *osrm.Client
	// Geocoder enables POST /geocode (address → coordinates). When nil,
	// that endpoint returns 503 and the UI works with coordinates only.
	Geocoder *geocode.Client
	// UI, when set, is served at GET / (expects an "index.html" entry).
	UI fs.FS
	// MaxStops rejects oversized requests. Default 100.
	MaxStops int
	// SolveTimeout bounds a single optimise call. Default 30s.
	SolveTimeout time.Duration
	// GeocodeTimeout bounds a single /geocode batch. Default 60s (batches
	// are rate-limited to ~1 address/sec).
	GeocodeTimeout time.Duration
	// Logger receives request logs. Default: log.Default().
	Logger *log.Logger
}

// NewServer builds a Server from Config.
func NewServer(cfg Config) *Server {
	if cfg.MaxStops <= 0 {
		cfg.MaxStops = 100
	}
	if cfg.SolveTimeout <= 0 {
		cfg.SolveTimeout = 30 * time.Second
	}
	if cfg.GeocodeTimeout <= 0 {
		cfg.GeocodeTimeout = 60 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	return &Server{
		osrm:           cfg.OSRM,
		geocoder:       cfg.Geocoder,
		ui:             cfg.UI,
		maxStops:       cfg.MaxStops,
		solveTimeout:   cfg.SolveTimeout,
		geocodeTimeout: cfg.GeocodeTimeout,
		logger:         cfg.Logger,
	}
}

// --- request / response DTOs ---

type stopDTO struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
}

type optimizeRequest struct {
	Stops  []stopDTO `json:"stops"`
	Metric string    `json:"metric"` // default "haversine"
	Mode   string    `json:"mode"`   // default "auto"
}

type legDTO struct {
	From string  `json:"from"`
	To   string  `json:"to"`
	Cost float64 `json:"cost"`
}

type optimizeResponse struct {
	Order        []string `json:"order"`
	TourIndices  []int    `json:"tour_indices"`
	Metric       string   `json:"metric"`
	Unit         string   `json:"unit"`
	TotalCost    float64  `json:"total_cost"`
	BaselineCost float64  `json:"baseline_cost"`
	SavedPercent float64  `json:"saved_percent"`
	Method       string   `json:"method"`
	RuntimeMs    float64  `json:"runtime_ms"`
	Legs         []legDTO `json:"legs"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// Handler returns the fully wired http.Handler: routes plus middleware
// (panic recovery, request logging).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /optimize", s.handleOptimize)
	mux.HandleFunc("POST /geocode", s.handleGeocode)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	if s.ui != nil {
		mux.HandleFunc("GET /{$}", s.handleUI) // exact "/" only
	}
	return s.recoverer(s.logRequests(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleUI(w http.ResponseWriter, _ *http.Request) {
	data, err := fs.ReadFile(s.ui, "index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "frontend not available")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// contextWithTimeout derives a request-scoped context with the given
// timeout, so a slow upstream (OSRM, Nominatim) can't hang a handler.
func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

func (s *Server) handleOptimize(w http.ResponseWriter, r *http.Request) {
	var req optimizeRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.Metric == "" {
		req.Metric = MetricHaversine
	}
	if req.Mode == "" {
		req.Mode = "auto"
	}

	points, err := s.toPoints(req.Stops)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	provider, err := s.providerFor(req.Metric)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errNoOSRM) {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.solveTimeout)
	defer cancel()

	cost, err := provider.Matrix(ctx, points)
	if err != nil {
		writeError(w, http.StatusBadGateway, "distance provider failed: "+err.Error())
		return
	}

	res, err := solveWithMode(cost.Values, req.Mode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, buildResponse(req.Metric, cost, points, res))
}

// solveWithMode dispatches to the requested solver strategy.
func solveWithMode(dist [][]float64, mode string) (solver.Result, error) {
	switch mode {
	case "auto":
		return solver.SolveMatrix(dist, solver.Options{})
	case "exact":
		return solver.SolveExact(dist)
	case "heuristic":
		return solver.SolveHeuristic(dist, solver.Options{})
	default:
		return solver.Result{}, errors.New(`unknown mode (want "auto", "exact" or "heuristic")`)
	}
}

// buildResponse assembles the JSON payload, including the input-order
// baseline and the per-leg breakdown, all in the cost matrix's unit.
func buildResponse(metric string, cost CostMatrix, points []solver.Point, res solver.Result) optimizeResponse {
	baseline := inputOrderCost(cost.Values)
	saved := 0.0
	if baseline > 0 {
		saved = (baseline - res.Distance) / baseline * 100
	}

	order := make([]string, len(res.Tour))
	for i, idx := range res.Tour {
		order[i] = points[idx].Name
	}

	legs := make([]legDTO, 0, len(res.Tour)-1)
	for i := 1; i < len(res.Tour); i++ {
		from, to := res.Tour[i-1], res.Tour[i]
		legs = append(legs, legDTO{
			From: points[from].Name,
			To:   points[to].Name,
			Cost: round1(cost.Values[from][to]),
		})
	}

	return optimizeResponse{
		Order:        order,
		TourIndices:  res.Tour,
		Metric:       metric,
		Unit:         cost.Unit,
		TotalCost:    round1(res.Distance),
		BaselineCost: round1(baseline),
		SavedPercent: round1(saved),
		Method:       res.Method,
		RuntimeMs:    float64(res.Runtime.Microseconds()) / 1000,
		Legs:         legs,
	}
}

// inputOrderCost measures the naive route: visit stops in submitted order,
// then return to the depot.
func inputOrderCost(dist [][]float64) float64 {
	total := 0.0
	for i := 0; i+1 < len(dist); i++ {
		total += dist[i][i+1]
	}
	return total + dist[len(dist)-1][0]
}

// toPoints validates the stops and converts them to solver.Points.
func (s *Server) toPoints(stops []stopDTO) ([]solver.Point, error) {
	if len(stops) < 2 {
		return nil, errors.New("need a depot and at least one stop (min 2 entries)")
	}
	if len(stops) > s.maxStops {
		return nil, errTooManyStops(len(stops), s.maxStops)
	}
	pts := make([]solver.Point, len(stops))
	for i, st := range stops {
		if st.Lat < -90 || st.Lat > 90 || st.Lng < -180 || st.Lng > 180 {
			return nil, badCoord(i, st.Lat, st.Lng)
		}
		name := st.Name
		if name == "" {
			name = defaultName(i)
		}
		pts[i] = solver.Point{Name: name, Lat: st.Lat, Lng: st.Lng}
	}
	return pts, nil
}

// --- middleware ---

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.logger.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Printf("panic on %s %s: %v", r.Method, r.URL.Path, rec)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
