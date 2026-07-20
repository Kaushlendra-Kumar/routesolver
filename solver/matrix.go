package solver

import "math"

// earthRadiusKm is the mean Earth radius (IUGG).
const earthRadiusKm = 6371.0088

// Haversine returns the great-circle distance between two points in
// kilometres. It is a lower bound on real road distance, which makes it a
// good default metric until a road-distance matrix (e.g. OSRM) is wired in.
func Haversine(a, b Point) float64 {
	const deg = math.Pi / 180
	lat1, lng1 := a.Lat*deg, a.Lng*deg
	lat2, lng2 := b.Lat*deg, b.Lng*deg

	dLat := lat2 - lat1
	dLng := lng2 - lng1

	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(h))
}

// BuildMatrix computes the symmetric pairwise haversine distance matrix.
func BuildMatrix(points []Point) [][]float64 {
	n := len(points)
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			d := Haversine(points[i], points[j])
			m[i][j] = d
			m[j][i] = d
		}
	}
	return m
}
