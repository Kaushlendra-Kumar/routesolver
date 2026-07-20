# routesolver

[![CI](https://github.com/Kaushlendra-Kumar/routesolver/actions/workflows/ci.yml/badge.svg)](https://github.com/Kaushlendra-Kumar/routesolver/actions/workflows/ci.yml)

A route optimisation engine and web app in pure Go (stdlib only, zero dependencies). Give it a depot and a list of stops; it returns the shortest round trip — **provably optimal** up to 18 stops via Held–Karp dynamic programming, and near-optimal beyond that via parallel multi-start 2-opt/Or-opt local search.

It ships end to end: a solver core, an OSRM road-distance integration, Nominatim geocoding, a JSON HTTP API, and an embedded Leaflet map frontend — the whole thing compiles to one self-contained binary. Paste addresses, get the fastest round trip drawn on a map.

## Quick start

```bash
# Web app (map + address input) — then open http://localhost:8080
go run ./cmd/routeserver

# Or with Docker (nothing else to install):
docker compose up --build
```

```bash
go test ./...                        # full test suite
go test -run '^$' -bench . ./solver  # benchmarks
go run ./cmd/routecli                # CLI demo on the 15-stop Bengaluru dataset
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

## HTTP API

`POST /optimize` wraps the solver as a service. Distances come from one of three metrics: local `haversine` (default, no network), `road_distance` (OSRM road km), or `road_duration` (OSRM travel minutes — optimise for the *fastest* round trip, not the shortest).

```bash
go run ./cmd/routeserver                        # haversine only, :8080
go run ./cmd/routeserver -osrm http://localhost:5000   # enable road metrics
go run ./cmd/routeserver -addr :9090 -max-stops 60
```

Request:

```bash
curl -X POST localhost:8080/optimize -H 'Content-Type: application/json' -d '{
  "metric": "haversine",
  "mode": "auto",
  "stops": [
    {"name": "Warehouse", "lat": 12.7105, "lng": 77.6960},
    {"name": "Whitefield", "lat": 12.9698, "lng": 77.7500},
    {"name": "Jayanagar", "lat": 12.9308, "lng": 77.5838}
  ]
}'
```

`metric` defaults to `haversine`, `mode` to `auto` (exact ≤18 stops, heuristic beyond). Response:

```json
{
  "order": ["Warehouse", "Jayanagar", "Whitefield", "Warehouse"],
  "tour_indices": [0, 2, 1, 0],
  "metric": "haversine",
  "unit": "km",
  "total_cost": 44.2,
  "baseline_cost": 51.8,
  "saved_percent": 14.7,
  "method": "held-karp",
  "runtime_ms": 0.1,
  "legs": [{"from": "Warehouse", "to": "Jayanagar", "cost": 15.9}, ...]
}
```

`unit` is `km` for distance metrics, `min` for `road_duration`, and every cost field is in that unit. Errors return `{"error": "..."}` with a matching status: 400 (bad input), 502 (OSRM upstream failed), 503 (road metric requested but no OSRM configured).

Endpoints: `POST /optimize`, `GET /healthz`. The server sets read/write/idle timeouts (Slowloris protection), logs every request, recovers from panics, and shuts down gracefully on SIGINT/SIGTERM, draining in-flight requests for up to 10s.

### Enabling road distances (OSRM)

The `road_*` metrics need an OSRM server. For a quick try, point at the public demo server (`-osrm https://router.project-osrm.org`) — rate-limited, driving-only, dev use only. For real use, self-host with a regional map extract:

```bash
wget http://download.geofabrik.de/asia/india/southern-zone-latest.osm.pbf
docker run -t -v "${PWD}:/data" osrm/osrm-backend osrm-extract -p /opt/car.lua /data/southern-zone-latest.osm.pbf
docker run -t -v "${PWD}:/data" osrm/osrm-backend osrm-partition  /data/southern-zone-latest.osrm
docker run -t -v "${PWD}:/data" osrm/osrm-backend osrm-customize  /data/southern-zone-latest.osrm
docker run -t -i -p 5000:5000 -v "${PWD}:/data" osrm/osrm-backend osrm-routed --algorithm mld /data/southern-zone-latest.osrm
```

One `/table` call returns the whole N×N road matrix, so the solver never changes — only its input does. OSRM coordinates are `lng,lat` (longitude first), which the client handles.

## Web UI

The server also ships a single-page frontend, embedded into the binary with `go:embed` — no separate static hosting, no build step, no npm. Just run the server and open it:

```bash
go run ./cmd/routeserver
# open http://localhost:8080
```

Type addresses (one per line, first line is the depot), pick a metric, hit optimise. The page geocodes the addresses via `POST /geocode`, drops pins, calls `POST /optimize`, then draws the optimised loop on an OpenStreetMap/Leaflet map with numbered stops and a per-leg cost breakdown. It comes pre-filled with a Bengaluru example so you can click optimise immediately.

Geocoding uses Nominatim (OpenStreetMap), which is free and keyless but rate-limited to 1 request/second by policy — the `geocode` package enforces that limit, caches results in memory, and sends the required `User-Agent`. Enabled by default; override with `-nominatim <url>` or disable with `-nominatim ""`. For anything past a demo, self-host Nominatim.

## Deploying

The whole app is one static binary (frontend embedded), so deployment is trivial:

```bash
go build -o routeserver ./cmd/routeserver
./routeserver -addr :8080
```

Or containerised — the multi-stage `Dockerfile` produces a ~5 MB static binary in a distroless image (no shell, non-root user, CA certs for HTTPS):

```bash
docker compose up --build        # → http://localhost:8080
```

That runs everywhere on free tiers (Fly.io, Railway, Render, a cheap VPS). Straight-line distance and geocoding work out of the box. Road distances need an OSRM server: run `./scripts/setup-osrm.sh` once to download and preprocess a regional map, then uncomment the `osrm` service in `docker-compose.yml` (or point `-osrm` at a self-hosted instance).

## Layout

```
solver/          library: types, haversine matrix, Held–Karp, heuristic
  solver.go      Solve / SolveMatrix / Options, auto-dispatch
  heldkarp.go    exact bitmask DP + path reconstruction
  heuristic.go   NN construction, 2-opt, Or-opt, parallel multi-start
  matrix.go      haversine + BuildMatrix
  *_test.go      brute-force oracle tests, quality bounds, determinism, benchmarks
osrm/            OSRM /table client → road distance/duration matrices
  client.go      lng,lat formatting, unit conversion, error handling
  client_test.go mock-server tests: parsing, coord order, error paths
geocode/         Nominatim client → address to coordinates
  nominatim.go   rate limiting (1 req/s), in-memory cache, User-Agent
  nominatim_test.go mock-server tests: parsing, caching, rate spacing
api/             HTTP service
  handler.go       POST /optimize, DTOs, validation, middleware, UI serving
  geocode_handler.go POST /geocode (batch address resolution)
  provider.go      MatrixProvider: haversine / road_distance / road_duration
  handler_test.go  end-to-end handler tests with a mock OSRM
web/             embedded single-page frontend (Leaflet map)
  web.go         go:embed the frontend into the binary
  index.html     address input → geocode → optimise → draw route
cmd/routecli/    CSV in → optimised route report or JSON out
cmd/routeserver/ HTTP server + embedded UI, graceful shutdown
data/            15-stop Bengaluru demo dataset
```

Testing approach: the DP is verified against an O(n!) brute-force oracle on every size 4–9; the heuristic is bounded against the exact optimum (can never beat it, must land within 5%); tours are structurally validated; heuristic runs are checked for seed-determinism.

## Roadmap

- **[done] Road distances:** OSRM `/table` matrix (self-hosted in Docker with a Geofabrik extract) behind `SolveMatrix` — `osrm` package, `road_distance` / `road_duration` metrics.
- **[done] HTTP API:** `POST /optimize` (JSON) — `api` package, `cmd/routeserver`.
- **[done] Geocoding:** address → lat/lng via Nominatim, rate-limited (1 req/s) and cached — `geocode` package, `POST /geocode`.
- **[done] Web UI:** embedded Leaflet single-page app — `web` package, served at `/`.
- **Result sharing:** Google Maps deep-link (split into ≤9-waypoint legs) + WhatsApp `wa.me` share; CSV upload in the UI.
- **Road geometry on the map:** OSRM `/route` for the actual road path between stops (currently the map draws straight segments in visit order).
- **Persistent cache:** swap the in-memory geocode cache for Redis so it survives restarts and scales across instances.
- **VRP extensions:** appointment time windows, multiple vehicles, capacity — a genuinely harder problem class, deliberately after v1.

## Publishing note

The module path is plain `routesolver` for local development. Before pushing to GitHub, rename it in one step:

```bash
go mod edit -module github.com/<your-username>/routesolver
find . -name '*.go' -exec sed -i 's|"routesolver/|"github.com/<your-username>/routesolver/|' {} +
```

## Resume bullet (suggested)

> Built a route-optimisation engine in Go: exact TSP via Held–Karp bitmask DP (provably optimal ≤18 stops, 34 ms for 15) with deterministic parallel multi-start 2-opt/Or-opt beyond (200 stops in <3 ms/start); cut demo route distance 59% vs input order; validated against a brute-force oracle; pluggable distance matrix for OSRM road data.
