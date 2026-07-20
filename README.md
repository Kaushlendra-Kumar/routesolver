# routesolver

A route optimisation engine in pure Go (stdlib only, zero dependencies). Give it a depot and a list of stops; it returns the shortest round trip — **provably optimal** up to 18 stops via Held–Karp dynamic programming, and near-optimal beyond that via parallel multi-start 2-opt/Or-opt local search.

This is the solver core for a delivery route planner ("paste your 15 addresses, get the fastest round trip"). Geocoding, road distances (OSRM) and an HTTP API layer on top of `SolveMatrix` — see the roadmap.

## Quick start

```bash
go test ./...                        # full test suite (~0.05s)
go test -run '^$' -bench . ./solver  # benchmarks
go run ./cmd/routecli                # demo on the 15-stop Bengaluru dataset
```

Demo output (`data/bangalore15.csv`, stops deliberately scrambled):

```
Route Solver — 15 stops + depot

Input-order distance :   293.9 km
Optimised distance   :   119.9 km   (59.2% shorter)
Method               : held-karp (provably optimal)
Solve time           : 33.36ms

Optimised order:
   1. Electronic City           + 15.5 km   ( 15.5 km total)
   2. HSR Layout                +  7.5 km   ( 23.0 km total)
   3. Koramangala               +  3.6 km   ( 26.6 km total)
   ...
  16. return to Warehouse (Anekal)  + 11.8 km   (119.9 km total)
```

Your own stops: a CSV of `name,lat,lng` where the first row is the depot.

```bash
go run ./cmd/routecli -in stops.csv                     # auto: exact ≤18 stops, heuristic beyond
go run ./cmd/routecli -in stops.csv -mode heuristic -starts 64
go run ./cmd/routecli -in stops.csv -json               # machine-readable output
```

## How it works

**Why not brute force?** 15 stops have 15! ≈ 1.3 trillion orderings. But you never enumerate routes.

**Exact — Held–Karp bitmask DP** (`solver/heldkarp.go`). `dp[mask][j]` = cheapest path from the depot that visits exactly the stops in `mask` and stands at stop `j`. That collapses 15! orderings into `2¹⁵ × 15²` ≈ 7.4M state transitions — solved in ~34 ms, and the answer is *provably* optimal, not a guess. Complexity O(n²·2ⁿ) time, O(n·2ⁿ) memory, which is also why it hits a wall: the benchmarks below show each extra stop roughly doubling the runtime. The dispatcher switches to the heuristic above 18 stops.

**Heuristic — multi-start 2.5-opt** (`solver/heuristic.go`). Build an initial tour with nearest-neighbour (or a random permutation), then descend: 2-opt removes edge crossings by reversing segments, Or-opt relocates runs of 1–3 stops that 2-opt can't express. Local search gets stuck in local optima, so we run many independent restarts with different starting tours and keep the best. Restarts are embarrassingly parallel — they fan out over a worker pool of goroutines, and per-restart RNG seeds are pre-drawn from a master seed so results are **deterministic** regardless of goroutine scheduling. In the test suite the heuristic independently reproduces the exact optimum on the 15-stop demo.

**Pluggable distances.** `Solve` defaults to haversine great-circle km, but everything runs through `SolveMatrix(dist, opts)` — hand it a real road-distance matrix (one call to OSRM's `/table` endpoint returns the whole matrix) and nothing else changes. Note: the local-search heuristic assumes a symmetric matrix; Held–Karp also handles asymmetric ones.

## Benchmarks

Measured on a single-core Intel Xeon 2.80 GHz container, Go 1.22 (`go test -run '^$' -bench . -benchmem ./solver`):

```
BenchmarkExact/stops=12                   2.80 ms/op      0.49 MB/op
BenchmarkExact/stops=15                  33.9  ms/op      4.9  MB/op
BenchmarkExact/stops=18                 359    ms/op     47    MB/op
BenchmarkHeuristicSingleStart/stops=50    0.060 ms/op
BenchmarkHeuristicSingleStart/stops=100   0.43  ms/op
BenchmarkHeuristicSingleStart/stops=200   2.7   ms/op
```

The exact rows are the exponential wall made visible: ×12 runtime for +3 stops. The multi-start benchmark (`BenchmarkMultiStart`) compares `workers=1` vs `workers=GOMAXPROCS`; run it on a multi-core machine to see near-linear wall-clock scaling (the CI container above has one core, so it shows none — honest numbers only).

## Layout

```
solver/          library: types, haversine matrix, Held–Karp, heuristic
  solver.go      Solve / SolveMatrix / Options, auto-dispatch
  heldkarp.go    exact bitmask DP + path reconstruction
  heuristic.go   NN construction, 2-opt, Or-opt, parallel multi-start
  matrix.go      haversine + BuildMatrix
  *_test.go      brute-force oracle tests, quality bounds, determinism, benchmarks
cmd/routecli/    CSV in → optimised route report or JSON out
data/            15-stop Bengaluru demo dataset
```

Testing approach: the DP is verified against an O(n!) brute-force oracle on every size 4–9; the heuristic is bounded against the exact optimum (can never beat it, must land within 5%); tours are structurally validated; heuristic runs are checked for seed-determinism.

## Roadmap

- **Road distances:** OSRM `/table` matrix (self-hosted in Docker with a Geofabrik extract) behind `SolveMatrix`.
- **Geocoding service:** address → lat/lng via Nominatim with a rate-limited worker pool (1 req/s) and Redis cache.
- **HTTP API:** `POST /optimize` accepting JSON or CSV upload; Google Maps deep-link + WhatsApp share of the result.
- **VRP extensions:** appointment time windows, multiple vehicles, capacity — a genuinely harder problem class, deliberately after v1.

## Publishing note

The module path is plain `routesolver` for local development. Before pushing to GitHub, rename it in one step:

```bash
go mod edit -module github.com/<your-username>/routesolver
find . -name '*.go' -exec sed -i 's|"routesolver/|"github.com/<your-username>/routesolver/|' {} +
```

## Resume bullet (suggested)

> Built a route-optimisation engine in Go: exact TSP via Held–Karp bitmask DP (provably optimal ≤18 stops, 34 ms for 15) with deterministic parallel multi-start 2-opt/Or-opt beyond (200 stops in <3 ms/start); cut demo route distance 59% vs input order; validated against a brute-force oracle; pluggable distance matrix for OSRM road data.
