// Package solver finds short round trips through a set of stops: the
// classic travelling-salesman problem with a fixed depot at index 0.
//
// Two strategies are provided and picked automatically by Solve /
// SolveMatrix:
//
//   - Held-Karp bitmask dynamic programming: provably optimal,
//     O(n² · 2ⁿ) time, practical up to ~19 nodes (depot + 18 stops).
//   - Multi-start nearest-neighbour construction + 2-opt + Or-opt local
//     search, spread across parallel workers, for larger inputs.
//
// Distances default to haversine great-circle kilometres, but any
// precomputed matrix (for example, real road distances or durations from
// OSRM's /table endpoint) can be supplied via SolveMatrix.
//
// Note: the local-search heuristic assumes a symmetric matrix
// (dist[i][j] == dist[j][i]). Held-Karp works for asymmetric matrices too.
package solver

import (
	"errors"
	"fmt"
	"runtime"
	"time"
)

// Point is a named geographic location.
type Point struct {
	Name string
	Lat  float64
	Lng  float64
}

// Result holds an optimised round trip.
type Result struct {
	// Tour lists node indices in visiting order. It always starts and
	// ends at 0 (the depot), so its length is number-of-nodes + 1.
	Tour []int
	// Distance is the total round-trip length, in the same unit as the
	// distance matrix (kilometres when built with BuildMatrix).
	Distance float64
	// Method is "held-karp" (exact) or "multi-start-2opt" (heuristic).
	Method string
	// Runtime is how long the solve took.
	Runtime time.Duration
}

// Options control solver behaviour. The zero value means "use defaults".
type Options struct {
	// MaxExactNodes is the largest node count (depot + stops) that the
	// automatic dispatcher will solve exactly. Default 19; hard-capped
	// at 20 to bound memory (Held-Karp memory grows as 2^(n-1)·(n-1)).
	MaxExactNodes int
	// Starts is how many independent restarts the heuristic performs.
	// Default: 4 × GOMAXPROCS.
	Starts int
	// Workers is how many restarts run concurrently. Default: GOMAXPROCS.
	Workers int
	// Seed makes heuristic runs reproducible. Default 1.
	Seed int64
}

// DefaultOptions returns the defaults described on Options.
func DefaultOptions() Options {
	return Options{
		MaxExactNodes: 19,
		Starts:        4 * runtime.GOMAXPROCS(0),
		Workers:       runtime.GOMAXPROCS(0),
		Seed:          1,
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.MaxExactNodes <= 0 {
		o.MaxExactNodes = d.MaxExactNodes
	}
	if o.MaxExactNodes > maxExactHardLimit {
		o.MaxExactNodes = maxExactHardLimit
	}
	if o.Starts <= 0 {
		o.Starts = d.Starts
	}
	if o.Workers <= 0 {
		o.Workers = d.Workers
	}
	if o.Seed == 0 {
		o.Seed = d.Seed
	}
	return o
}

// Solve optimises a round trip over points, where points[0] is the depot.
// Distances are haversine kilometres. Small inputs are solved exactly,
// larger ones heuristically (see Options.MaxExactNodes).
func Solve(points []Point, opts Options) (Result, error) {
	if len(points) < 2 {
		return Result{}, errors.New("solver: need a depot and at least one stop")
	}
	return SolveMatrix(BuildMatrix(points), opts)
}

// SolveMatrix is like Solve but takes a precomputed distance matrix, which
// lets callers plug in real road distances (e.g. from OSRM) instead of
// straight-line haversine. dist[0] must correspond to the depot.
func SolveMatrix(dist [][]float64, opts Options) (Result, error) {
	if err := validate(dist); err != nil {
		return Result{}, err
	}
	opts = opts.withDefaults()

	start := time.Now()
	var res Result
	if len(dist) <= opts.MaxExactNodes {
		res = solveHeldKarp(dist)
	} else {
		res = solveMultiStart(dist, opts)
	}
	res.Runtime = time.Since(start)
	return res, nil
}

// SolveHeuristic always uses the multi-start local-search heuristic,
// regardless of problem size.
func SolveHeuristic(dist [][]float64, opts Options) (Result, error) {
	if err := validate(dist); err != nil {
		return Result{}, err
	}
	opts = opts.withDefaults()

	start := time.Now()
	res := solveMultiStart(dist, opts)
	res.Runtime = time.Since(start)
	return res, nil
}

func validate(dist [][]float64) error {
	n := len(dist)
	if n < 2 {
		return errors.New("solver: need at least a depot and one stop")
	}
	for i, row := range dist {
		if len(row) != n {
			return fmt.Errorf("solver: distance matrix is not square (row %d has %d entries, want %d)", i, len(row), n)
		}
	}
	return nil
}
