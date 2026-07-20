package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Kaushlendra-Kumar/routesolver/geocode"
)

var errNoGeocoder = errors.New("geocoding is not configured on this server")

type geocodeRequest struct {
	Addresses []string `json:"addresses"`
}

type geocodeResponse struct {
	Results []geocode.Result `json:"results"`
}

// handleGeocode resolves a batch of addresses to coordinates, preserving
// input order and returning a Found flag per address. Rate limiting and
// caching live in the geocode client.
func (s *Server) handleGeocode(w http.ResponseWriter, r *http.Request) {
	if s.geocoder == nil {
		writeError(w, http.StatusServiceUnavailable, errNoGeocoder.Error())
		return
	}

	var req geocodeRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(req.Addresses) == 0 {
		writeError(w, http.StatusBadRequest, "provide at least one address")
		return
	}
	if len(req.Addresses) > s.maxStops {
		writeError(w, http.StatusBadRequest, errTooManyStops(len(req.Addresses), s.maxStops).Error())
		return
	}

	ctx, cancel := contextWithTimeout(r, s.geocodeTimeout)
	defer cancel()

	results, err := s.geocoder.GeocodeMany(ctx, req.Addresses)
	if err != nil {
		writeError(w, http.StatusBadGateway, "geocoding failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, geocodeResponse{Results: results})
}
