package solver

import (
	"fmt"
	"math/rand"
	"runtime"
	"testing"
)

func benchMatrix(nodes int, seed int64) [][]float64 {
	rng := rand.New(rand.NewSource(seed))
	return BuildMatrix(randomPoints(nodes, rng))
}

// BenchmarkExact shows Held-Karp's exponential wall: every extra stop
// roughly doubles the work, which is exactly why the dispatcher switches
// to the heuristic beyond 18 stops.
func BenchmarkExact(b *testing.B) {
	for _, stops := range []int{12, 15, 18} {
		dist := benchMatrix(stops+1, 1)
		b.Run(fmt.Sprintf("stops=%d", stops), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := SolveExact(dist); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkHeuristicSingleStart measures one nearest-neighbour + 2-opt +
// Or-opt descent at sizes the exact solver could never touch.
func BenchmarkHeuristicSingleStart(b *testing.B) {
	for _, stops := range []int{50, 100, 200} {
		dist := benchMatrix(stops+1, 2)
		opts := Options{Starts: 1, Workers: 1, Seed: 3}
		b.Run(fmt.Sprintf("stops=%d", stops), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := SolveHeuristic(dist, opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMultiStart compares 16 restarts on one worker vs all CPUs —
// restarts are embarrassingly parallel, so on a multi-core machine the
// wall-clock time drops near-linearly with cores.
func BenchmarkMultiStart(b *testing.B) {
	dist := benchMatrix(121, 4) // 120 stops
	for _, w := range []int{1, runtime.GOMAXPROCS(0)} {
		opts := Options{Starts: 16, Workers: w, Seed: 5}
		b.Run(fmt.Sprintf("workers=%d", w), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := SolveHeuristic(dist, opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
