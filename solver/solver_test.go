package solver

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

// randomPoints scatters n named points around the Bengaluru bounding box.
func randomPoints(n int, rng *rand.Rand) []Point {
	pts := make([]Point, n)
	for i := range pts {
		pts[i] = Point{
			Name: fmt.Sprintf("P%d", i),
			Lat:  12.7 + rng.Float64()*0.4,
			Lng:  77.4 + rng.Float64()*0.4,
		}
	}
	return pts
}

// bruteForce enumerates every permutation of the stops — the O(n!) oracle
// the DP is tested against. Only sane for small n.
func bruteForce(dist [][]float64) float64 {
	n := len(dist)
	stops := make([]int, n-1)
	for i := range stops {
		stops[i] = i + 1
	}
	best := math.Inf(1)

	var permute func(k int)
	permute = func(k int) {
		if k == len(stops) {
			d := dist[0][stops[0]]
			for i := 0; i+1 < len(stops); i++ {
				d += dist[stops[i]][stops[i+1]]
			}
			d += dist[stops[len(stops)-1]][0]
			if d < best {
				best = d
			}
			return
		}
		for i := k; i < len(stops); i++ {
			stops[k], stops[i] = stops[i], stops[k]
			permute(k + 1)
			stops[k], stops[i] = stops[i], stops[k]
		}
	}
	permute(0)
	return best
}

func checkValidTour(t *testing.T, tour []int, n int) {
	t.Helper()
	if len(tour) != n+1 || tour[0] != 0 || tour[n] != 0 {
		t.Fatalf("bad tour shape (want depot at both ends, length %d): %v", n+1, tour)
	}
	seen := make([]bool, n)
	seen[0] = true
	for _, v := range tour[1:n] {
		if v < 1 || v >= n || seen[v] {
			t.Fatalf("tour is not a permutation of stops: %v", tour)
		}
		seen[v] = true
	}
}

func TestHaversine(t *testing.T) {
	blr := Point{Name: "Bengaluru", Lat: 12.9716, Lng: 77.5946}
	bom := Point{Name: "Mumbai", Lat: 19.0760, Lng: 72.8777}

	if d := Haversine(blr, blr); d != 0 {
		t.Fatalf("distance to self = %v, want 0", d)
	}
	d := Haversine(blr, bom)
	if d < 830 || d > 860 {
		t.Fatalf("Bengaluru→Mumbai = %.1f km, want ≈845 km", d)
	}
	if got := Haversine(bom, blr); math.Abs(got-d) > 1e-9 {
		t.Fatalf("haversine not symmetric: %v vs %v", d, got)
	}
}

// TestExactKnownSquare uses a hand-built matrix: four corners of a unit
// square, where the optimal cycle is obviously the perimeter (length 4).
func TestExactKnownSquare(t *testing.T) {
	s2 := math.Sqrt2
	dist := [][]float64{
		{0, 1, s2, 1},
		{1, 0, 1, s2},
		{s2, 1, 0, 1},
		{1, s2, 1, 0},
	}
	res, err := SolveExact(dist)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.Distance-4) > 1e-9 {
		t.Fatalf("square tour = %v, want 4 (perimeter)", res.Distance)
	}
	checkValidTour(t, res.Tour, 4)
}

func TestExactMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for n := 4; n <= 9; n++ {
		for trial := 0; trial < 3; trial++ {
			dist := BuildMatrix(randomPoints(n, rng))
			want := bruteForce(dist)
			got, err := SolveExact(dist)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got.Distance-want) > 1e-6 {
				t.Fatalf("n=%d: held-karp=%.6f, brute-force=%.6f", n, got.Distance, want)
			}
			checkValidTour(t, got.Tour, n)
			if l := tourLength(dist, got.Tour); math.Abs(l-got.Distance) > 1e-6 {
				t.Fatalf("n=%d: reported %.6f but tour measures %.6f", n, got.Distance, l)
			}
		}
	}
}

// TestHeuristicNearOptimal checks the multi-start heuristic against the
// exact answer on instances small enough to solve both ways. It can never
// beat the optimum, and with 24 restarts it should land within 5% of it.
func TestHeuristicNearOptimal(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 5; trial++ {
		dist := BuildMatrix(randomPoints(14, rng))
		exact, err := SolveExact(dist)
		if err != nil {
			t.Fatal(err)
		}
		heur, err := SolveHeuristic(dist, Options{Starts: 24, Seed: 99})
		if err != nil {
			t.Fatal(err)
		}
		checkValidTour(t, heur.Tour, 14)
		if heur.Distance < exact.Distance-1e-6 {
			t.Fatalf("heuristic %.6f beat the optimum %.6f — impossible, solver bug", heur.Distance, exact.Distance)
		}
		if heur.Distance > exact.Distance*1.05 {
			t.Errorf("trial %d: heuristic %.3f is >5%% above optimum %.3f", trial, heur.Distance, exact.Distance)
		}
	}
}

func TestHeuristicDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	dist := BuildMatrix(randomPoints(40, rng))
	opts := Options{Starts: 8, Workers: 4, Seed: 1234}

	a, err := SolveHeuristic(dist, opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SolveHeuristic(dist, opts)
	if err != nil {
		t.Fatal(err)
	}
	if a.Distance != b.Distance || !reflect.DeepEqual(a.Tour, b.Tour) {
		t.Fatalf("same seed gave different results:\n%v (%.4f)\n%v (%.4f)", a.Tour, a.Distance, b.Tour, b.Distance)
	}
}

func TestSolveMatrixDispatch(t *testing.T) {
	rng := rand.New(rand.NewSource(3))

	small, err := SolveMatrix(BuildMatrix(randomPoints(10, rng)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if small.Method != "held-karp" {
		t.Fatalf("10 nodes dispatched to %q, want held-karp", small.Method)
	}

	large, err := SolveMatrix(BuildMatrix(randomPoints(25, rng)), Options{Seed: 5})
	if err != nil {
		t.Fatal(err)
	}
	if large.Method != "multi-start-2opt" {
		t.Fatalf("25 nodes dispatched to %q, want multi-start-2opt", large.Method)
	}
	checkValidTour(t, large.Tour, 25)
}

func TestTwoStopTrivial(t *testing.T) {
	pts := []Point{
		{Name: "Home", Lat: 12.71, Lng: 77.69},
		{Name: "A", Lat: 12.90, Lng: 77.60},
	}
	res, err := Solve(pts, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := 2 * Haversine(pts[0], pts[1])
	if math.Abs(res.Distance-want) > 1e-9 {
		t.Fatalf("round trip = %v, want %v", res.Distance, want)
	}
}

func TestErrors(t *testing.T) {
	if _, err := Solve([]Point{{Name: "only-depot"}}, Options{}); err == nil {
		t.Fatal("expected error for a single point")
	}
	bad := [][]float64{{0, 1}, {1}}
	if _, err := SolveMatrix(bad, Options{}); err == nil {
		t.Fatal("expected error for a ragged matrix")
	}
	huge := make([][]float64, 30)
	for i := range huge {
		huge[i] = make([]float64, 30)
	}
	if _, err := SolveExact(huge); err == nil {
		t.Fatal("expected error for exact solve above the hard limit")
	}
}
