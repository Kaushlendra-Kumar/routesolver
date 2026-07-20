package solver

import (
	"errors"
	"math"
	"time"
)

// maxExactHardLimit caps Held-Karp problem size. The DP table needs
// 2^(n-1) · (n-1) float64 entries plus an int16 parent table:
// n=16 → ~4 MB, n=19 → ~40 MB, n=20 → ~100 MB, n=22 would be ~450 MB.
const maxExactHardLimit = 20

// SolveExact runs the Held-Karp dynamic program and returns a provably
// optimal round trip. Exponential in the number of nodes, so it refuses
// inputs above maxExactHardLimit nodes (depot included); use
// SolveHeuristic for those.
func SolveExact(dist [][]float64) (Result, error) {
	if err := validate(dist); err != nil {
		return Result{}, err
	}
	if len(dist) > maxExactHardLimit {
		return Result{}, errors.New("solver: too many nodes for the exact solver, use SolveHeuristic")
	}
	start := time.Now()
	res := solveHeldKarp(dist)
	res.Runtime = time.Since(start)
	return res, nil
}

// solveHeldKarp implements the classic Held-Karp bitmask DP.
//
// Stops 1..n-1 are mapped to bits 0..m-1 (m = n-1). dp[mask][j] is the
// cheapest path that starts at the depot, visits exactly the stops in
// mask, and currently stands at stop j. Transitions extend a path by one
// unvisited stop, so each of the 2^m masks is expanded over m² pairs:
// O(m² · 2^m) time, O(m · 2^m) memory. The parent table lets us walk the
// optimal tour back out at the end.
func solveHeldKarp(dist [][]float64) Result {
	n := len(dist)
	m := n - 1 // number of stops (excluding depot)
	size := 1 << m
	inf := math.Inf(1)

	// Flat tables indexed as [mask*m + j] to avoid per-row slice overhead.
	dp := make([]float64, size*m)
	par := make([]int16, size*m)
	for i := range dp {
		dp[i] = inf
	}

	// Base case: depot → single stop j.
	for j := 0; j < m; j++ {
		idx := (1<<j)*m + j
		dp[idx] = dist[0][j+1]
		par[idx] = -1
	}

	for mask := 1; mask < size; mask++ {
		base := mask * m
		for j := 0; j < m; j++ {
			if mask&(1<<j) == 0 {
				continue
			}
			cur := dp[base+j]
			if math.IsInf(cur, 1) {
				continue
			}
			row := dist[j+1]
			for k := 0; k < m; k++ {
				if mask&(1<<k) != 0 {
					continue
				}
				next := mask | 1<<k
				idx := next*m + k
				if cand := cur + row[k+1]; cand < dp[idx] {
					dp[idx] = cand
					par[idx] = int16(j)
				}
			}
		}
	}

	// Close the tour: best "visited everything, standing at j" + j → depot.
	full := size - 1
	best, bestEnd := inf, -1
	for j := 0; j < m; j++ {
		if c := dp[full*m+j] + dist[j+1][0]; c < best {
			best = c
			bestEnd = j
		}
	}

	// Reconstruct by walking parents from the final stop back to the first.
	rev := make([]int, 0, m)
	mask, j := full, bestEnd
	for j >= 0 {
		rev = append(rev, j+1)
		p := int(par[mask*m+j])
		mask &^= 1 << j
		j = p
	}

	tour := make([]int, 0, n+1)
	tour = append(tour, 0)
	for i := len(rev) - 1; i >= 0; i-- {
		tour = append(tour, rev[i])
	}
	tour = append(tour, 0)

	return Result{Tour: tour, Distance: best, Method: "held-karp"}
}
