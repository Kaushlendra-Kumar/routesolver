package api

import (
	"context"
	"fmt"

	"github.com/Kaushlendra-Kumar/routesolver/osrm"
	"github.com/Kaushlendra-Kumar/routesolver/solver"
)

// CostMatrix is the matrix the solver minimises, tagged with the unit its
// numbers are in so responses can be labelled correctly.
type CostMatrix struct {
	Values [][]float64
	Unit   string // "km" or "min"
}

// MatrixProvider turns a set of points into a cost matrix. Keeping this an
// interface lets the HTTP handler stay ignorant of whether distances come
// from local haversine maths or a network call to OSRM — and lets tests
// inject a canned matrix instead of standing up a fake server.
type MatrixProvider interface {
	Matrix(ctx context.Context, pts []solver.Point) (CostMatrix, error)
}

// HaversineProvider computes straight-line great-circle distances locally.
// No network, no dependencies — the default and the fallback.
type HaversineProvider struct{}

func (HaversineProvider) Matrix(_ context.Context, pts []solver.Point) (CostMatrix, error) {
	return CostMatrix{Values: solver.BuildMatrix(pts), Unit: "km"}, nil
}

// RoadDistanceProvider uses OSRM road distances (kilometres).
type RoadDistanceProvider struct{ Client *osrm.Client }

func (p RoadDistanceProvider) Matrix(ctx context.Context, pts []solver.Point) (CostMatrix, error) {
	m, err := p.Client.Table(ctx, pts)
	if err != nil {
		return CostMatrix{}, err
	}
	return CostMatrix{Values: m.DistanceKm, Unit: "km"}, nil
}

// RoadDurationProvider uses OSRM travel times (minutes) — optimise for the
// fastest round trip rather than the shortest.
type RoadDurationProvider struct{ Client *osrm.Client }

func (p RoadDurationProvider) Matrix(ctx context.Context, pts []solver.Point) (CostMatrix, error) {
	m, err := p.Client.Table(ctx, pts)
	if err != nil {
		return CostMatrix{}, err
	}
	return CostMatrix{Values: m.DurationMin, Unit: "min"}, nil
}

// Metric names the selectable optimisation targets accepted by the API.
const (
	MetricHaversine    = "haversine"
	MetricRoadDistance = "road_distance"
	MetricRoadDuration = "road_duration"
)

// providerFor maps a request metric to a provider. Road metrics need a
// configured OSRM client; asking for one without it is a clear error
// rather than a silent fallback.
func (s *Server) providerFor(metric string) (MatrixProvider, error) {
	switch metric {
	case MetricHaversine:
		return HaversineProvider{}, nil
	case MetricRoadDistance:
		if s.osrm == nil {
			return nil, errNoOSRM
		}
		return RoadDistanceProvider{Client: s.osrm}, nil
	case MetricRoadDuration:
		if s.osrm == nil {
			return nil, errNoOSRM
		}
		return RoadDurationProvider{Client: s.osrm}, nil
	default:
		return nil, fmt.Errorf("unknown metric %q (want %s, %s or %s)",
			metric, MetricHaversine, MetricRoadDistance, MetricRoadDuration)
	}
}
