// Command routecli optimises a round-trip route over stops in a CSV file.
//
// CSV format: name,lat,lng — one row per location, first row is the depot
// (where the trip starts and ends). A header row is detected and skipped.
//
// Examples:
//
//	go run ./cmd/routecli
//	go run ./cmd/routecli -in data/bangalore15.csv -mode exact
//	go run ./cmd/routecli -in stops.csv -mode heuristic -starts 64 -json
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Kaushlendra-Kumar/routesolver/solver"
)

func main() {
	var (
		in      = flag.String("in", "data/bangalore15.csv", "input CSV: name,lat,lng (first row = depot)")
		mode    = flag.String("mode", "auto", "auto | exact | heuristic")
		starts  = flag.Int("starts", 0, "heuristic restarts (0 = default)")
		workers = flag.Int("workers", 0, "parallel workers (0 = all CPUs)")
		seed    = flag.Int64("seed", 1, "heuristic RNG seed (reproducible runs)")
		asJSON  = flag.Bool("json", false, "print machine-readable JSON instead of the report")
	)
	flag.Parse()

	points, err := readCSV(*in)
	check(err)

	dist := solver.BuildMatrix(points)
	opts := solver.Options{Starts: *starts, Workers: *workers, Seed: *seed}

	var res solver.Result
	switch *mode {
	case "auto":
		res, err = solver.SolveMatrix(dist, opts)
	case "exact":
		res, err = solver.SolveExact(dist)
	case "heuristic":
		res, err = solver.SolveHeuristic(dist, opts)
	default:
		err = fmt.Errorf("unknown -mode %q (want auto, exact or heuristic)", *mode)
	}
	check(err)

	baseline := inputOrderDistance(dist)
	if *asJSON {
		check(printJSON(points, res, baseline))
		return
	}
	printReport(points, dist, res, baseline)
}

// readCSV parses name,lat,lng rows; the first data row is the depot.
func readCSV(path string) ([]solver.Point, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true
	recs, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var pts []solver.Point
	for i, rec := range recs {
		if len(rec) < 3 {
			return nil, fmt.Errorf("%s row %d: want name,lat,lng", path, i+1)
		}
		lat, errLat := strconv.ParseFloat(strings.TrimSpace(rec[1]), 64)
		lng, errLng := strconv.ParseFloat(strings.TrimSpace(rec[2]), 64)
		if errLat != nil || errLng != nil {
			if i == 0 {
				continue // header row
			}
			return nil, fmt.Errorf("%s row %d: bad coordinates %q,%q", path, i+1, rec[1], rec[2])
		}
		pts = append(pts, solver.Point{Name: strings.TrimSpace(rec[0]), Lat: lat, Lng: lng})
	}
	if len(pts) < 2 {
		return nil, fmt.Errorf("%s: need a depot and at least one stop", path)
	}
	return pts, nil
}

// inputOrderDistance measures the naive route: visit stops in file order.
func inputOrderDistance(dist [][]float64) float64 {
	total := 0.0
	for i := 0; i+1 < len(dist); i++ {
		total += dist[i][i+1]
	}
	return total + dist[len(dist)-1][0]
}

func printReport(points []solver.Point, dist [][]float64, res solver.Result, baseline float64) {
	saved := (baseline - res.Distance) / baseline * 100
	method := res.Method
	if method == "held-karp" {
		method += " (provably optimal)"
	}

	fmt.Printf("Route Solver — %d stops + depot\n\n", len(points)-1)
	fmt.Printf("Input-order distance : %7.1f km\n", baseline)
	fmt.Printf("Optimised distance   : %7.1f km   (%.1f%% shorter)\n", res.Distance, saved)
	fmt.Printf("Method               : %s\n", method)
	fmt.Printf("Solve time           : %s\n\n", res.Runtime.Round(10*time.Microsecond))

	fmt.Println("Optimised order:")
	cum := 0.0
	for i := 1; i < len(res.Tour); i++ {
		from, to := res.Tour[i-1], res.Tour[i]
		leg := dist[from][to]
		cum += leg
		name := points[to].Name
		if to == 0 {
			name = "return to " + points[0].Name
		}
		fmt.Printf("  %2d. %-24s  +%5.1f km   (%5.1f km total)\n", i, name, leg, cum)
	}
}

func printJSON(points []solver.Point, res solver.Result, baseline float64) error {
	names := make([]string, len(res.Tour))
	for i, idx := range res.Tour {
		names[i] = points[idx].Name
	}
	out := struct {
		Order        []string `json:"order"`
		TourIndices  []int    `json:"tour_indices"`
		DistanceKm   float64  `json:"distance_km"`
		BaselineKm   float64  `json:"baseline_km"`
		SavedPercent float64  `json:"saved_percent"`
		Method       string   `json:"method"`
		RuntimeMs    float64  `json:"runtime_ms"`
	}{
		Order:        names,
		TourIndices:  res.Tour,
		DistanceKm:   res.Distance,
		BaselineKm:   baseline,
		SavedPercent: (baseline - res.Distance) / baseline * 100,
		Method:       res.Method,
		RuntimeMs:    float64(res.Runtime.Microseconds()) / 1000,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "routecli:", err)
		os.Exit(1)
	}
}
