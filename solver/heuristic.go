package solver

import (
	"math"
	"math/rand"
	"sync"
)

// tourLength sums a closed tour (tour[0] == tour[len-1] == depot).
func tourLength(dist [][]float64, tour []int) float64 {
	total := 0.0
	for i := 0; i+1 < len(tour); i++ {
		total += dist[tour[i]][tour[i+1]]
	}
	return total
}

// nearestNeighbor builds an initial tour greedily: always drive to the
// closest unvisited stop. If first > 0, that stop is forced as the first
// visit (used to diversify multi-start restarts).
func nearestNeighbor(dist [][]float64, first int) []int {
	n := len(dist)
	visited := make([]bool, n)
	tour := make([]int, 0, n+1)

	tour = append(tour, 0)
	visited[0] = true
	cur := 0
	if first > 0 {
		tour = append(tour, first)
		visited[first] = true
		cur = first
	}
	for len(tour) < n {
		next, bestD := -1, math.Inf(1)
		for j := 1; j < n; j++ {
			if !visited[j] && dist[cur][j] < bestD {
				bestD = dist[cur][j]
				next = j
			}
		}
		tour = append(tour, next)
		visited[next] = true
		cur = next
	}
	return append(tour, 0)
}

// randomTour returns depot + a random permutation of the stops + depot.
func randomTour(n int, rng *rand.Rand) []int {
	tour := make([]int, 0, n+1)
	tour = append(tour, 0)
	for _, p := range rng.Perm(n - 1) {
		tour = append(tour, p+1)
	}
	return append(tour, 0)
}

// twoOpt improves a tour in place: while any pair of edges (a-b, c-d) can
// be replaced by (a-c, b-d) for a shorter tour, reverse the segment
// between them. First-improvement strategy; loops until a full pass finds
// nothing. The depot endpoints are fixed. Assumes a symmetric matrix.
func twoOpt(dist [][]float64, tour []int) {
	n := len(tour)
	improved := true
	for improved {
		improved = false
		for i := 1; i < n-2; i++ {
			a, b := tour[i-1], tour[i]
			dab := dist[a][b]
			for k := i + 1; k < n-1; k++ {
				c, d := tour[k], tour[k+1]
				delta := dist[a][c] + dist[b][d] - dab - dist[c][d]
				if delta < -1e-9 {
					reverse(tour, i, k)
					improved = true
					b = tour[i]
					dab = dist[a][b]
				}
			}
		}
	}
}

func reverse(t []int, i, k int) {
	for i < k {
		t[i], t[k] = t[k], t[i]
		i++
		k--
	}
}

// orOptPass tries to relocate short segments (1–3 consecutive stops) to a
// better position elsewhere in the tour — moves that 2-opt cannot express.
// Applies the first improving move found and returns true; returns false
// if no improving move exists.
func orOptPass(dist [][]float64, tour []int) bool {
	n := len(tour)
	for segLen := 1; segLen <= 3; segLen++ {
		for i := 1; i+segLen-1 <= n-2; i++ {
			j := i + segLen - 1
			prev, next := tour[i-1], tour[j+1]
			s1, s2 := tour[i], tour[j]

			removeGain := dist[prev][s1] + dist[s2][next] - dist[prev][next]
			if removeGain <= 1e-9 {
				continue // removing this segment doesn't shorten anything
			}
			for p := 0; p < n-1; p++ {
				if p >= i-1 && p <= j {
					continue // edge inside or adjacent to the segment
				}
				a, b := tour[p], tour[p+1]
				insertCost := dist[a][s1] + dist[s2][b] - dist[a][b]
				if insertCost < removeGain-1e-9 {
					moveSegment(tour, i, j, p)
					return true
				}
			}
		}
	}
	return false
}

// moveSegment removes tour[i..j] and re-inserts it after position p
// (p indexes the tour before removal; p is never inside [i-1, j]).
func moveSegment(t []int, i, j, p int) {
	seg := append([]int(nil), t[i:j+1]...)
	rest := make([]int, 0, len(t)-len(seg))
	rest = append(rest, t[:i]...)
	rest = append(rest, t[j+1:]...)

	pos := p
	if p > j {
		pos -= len(seg)
	}

	out := make([]int, 0, len(t))
	out = append(out, rest[:pos+1]...)
	out = append(out, seg...)
	out = append(out, rest[pos+1:]...)
	copy(t, out)
}

// improve runs 2-opt to a local optimum, then alternates Or-opt moves and
// 2-opt until neither finds anything ("2.5-opt").
func improve(dist [][]float64, tour []int) {
	twoOpt(dist, tour)
	for orOptPass(dist, tour) {
		twoOpt(dist, tour)
	}
}

// solveMultiStart runs Options.Starts independent restarts — one pure
// nearest-neighbour, the rest seeded with a random first stop or a fully
// random permutation — improves each with 2-opt + Or-opt, and keeps the
// best. Restarts are independent, so they fan out over Options.Workers
// goroutines; results are collected by index so the outcome is
// deterministic for a given Options.Seed regardless of scheduling.
func solveMultiStart(dist [][]float64, opts Options) Result {
	n := len(dist)
	starts := opts.Starts
	workers := opts.Workers
	if workers > starts {
		workers = starts
	}

	// Pre-draw one seed per restart from a master RNG so each restart is
	// reproducible independent of which worker picks it up.
	master := rand.New(rand.NewSource(opts.Seed))
	seeds := make([]int64, starts)
	for i := range seeds {
		seeds[i] = master.Int63()
	}

	results := make([]Result, starts)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				rng := rand.New(rand.NewSource(seeds[idx]))
				var tour []int
				switch {
				case idx == 0:
					tour = nearestNeighbor(dist, -1)
				case idx%2 == 1:
					tour = nearestNeighbor(dist, 1+rng.Intn(n-1))
				default:
					tour = randomTour(n, rng)
				}
				improve(dist, tour)
				results[idx] = Result{Tour: tour, Distance: tourLength(dist, tour)}
			}
		}()
	}
	for i := 0; i < starts; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	best := results[0]
	for _, r := range results[1:] {
		if r.Distance < best.Distance-1e-9 {
			best = r
		}
	}
	best.Method = "multi-start-2opt"
	return best
}
