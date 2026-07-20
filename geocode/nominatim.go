// Package geocode turns street addresses into coordinates using a
// Nominatim (OpenStreetMap) server.
//
// The public Nominatim instance is free and needs no API key, but its
// usage policy requires: a valid identifying User-Agent, at most one
// request per second, and no heavy/bulk use. This client enforces the
// rate limit and caches results in memory so repeated addresses are free.
// For anything beyond a demo, self-host Nominatim or use a paid geocoder
// and point baseURL at it.
package geocode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PublicServer is the free OpenStreetMap Nominatim instance. Demo/low
// volume only — respect the 1 req/s policy (this client does).
const PublicServer = "https://nominatim.openstreetmap.org"

// Result is the outcome of geocoding one address. Found is false when the
// geocoder returned no match; that is not an error.
type Result struct {
	Address     string  `json:"address"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	DisplayName string  `json:"display_name,omitempty"`
	Found       bool    `json:"found"`
}

// Client is a rate-limited, caching Nominatim client. Construct with New.
type Client struct {
	baseURL string
	ua      string
	http    *http.Client

	minInterval time.Duration
	rateMu      sync.Mutex
	lastCall    time.Time

	cacheMu sync.RWMutex
	cache   map[string]Result
}

// New returns a Client. userAgent must identify your app (Nominatim policy).
// A zero timeout defaults to 10s; a zero minInterval defaults to 1s.
func New(baseURL, userAgent string, timeout, minInterval time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if minInterval <= 0 {
		minInterval = time.Second
	}
	if userAgent == "" {
		userAgent = "routesolver/1.0"
	}
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		ua:          userAgent,
		http:        &http.Client{Timeout: timeout},
		minInterval: minInterval,
		cache:       make(map[string]Result),
	}
}

// nominatimHit mirrors one element of Nominatim's /search JSON array.
// Nominatim returns lat/lon as strings, hence the parsing below.
type nominatimHit struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
}

// Geocode resolves a single address. A cache hit skips both the network
// and the rate limiter. A no-match returns Result{Found:false}, nil.
func (c *Client) Geocode(ctx context.Context, address string) (Result, error) {
	key := cacheKey(address)
	if key == "" {
		return Result{Address: address, Found: false}, nil
	}
	if r, ok := c.cached(key); ok {
		return r, nil
	}

	// Only real network calls are rate-limited.
	if err := c.waitRate(ctx); err != nil {
		return Result{}, err
	}

	q := url.Values{}
	q.Set("q", address)
	q.Set("format", "jsonv2")
	q.Set("limit", "1")
	reqURL := c.baseURL + "/search?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return Result{}, fmt.Errorf("geocode: building request: %w", err)
	}
	req.Header.Set("User-Agent", c.ua) // required by Nominatim

	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("geocode: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("geocode: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("geocode: server returned %s", resp.Status)
	}

	var hits []nominatimHit
	if err := json.Unmarshal(body, &hits); err != nil {
		return Result{}, fmt.Errorf("geocode: decoding response: %w", err)
	}

	result := Result{Address: address, Found: false}
	if len(hits) > 0 {
		lat, errLat := strconv.ParseFloat(hits[0].Lat, 64)
		lng, errLng := strconv.ParseFloat(hits[0].Lon, 64)
		if errLat == nil && errLng == nil {
			result = Result{
				Address:     address,
				Lat:         lat,
				Lng:         lng,
				DisplayName: hits[0].DisplayName,
				Found:       true,
			}
		}
	}
	c.store(key, result)
	return result, nil
}

// GeocodeMany resolves addresses in order. Cached and duplicate addresses
// don't re-hit the network, so the 1 req/s cost is paid once per distinct
// new address. A transport error aborts the batch; no-matches are returned
// inline with Found:false.
func (c *Client) GeocodeMany(ctx context.Context, addresses []string) ([]Result, error) {
	out := make([]Result, len(addresses))
	for i, addr := range addresses {
		r, err := c.Geocode(ctx, addr)
		if err != nil {
			return nil, err
		}
		out[i] = r
	}
	return out, nil
}

// waitRate blocks until at least minInterval has elapsed since the last
// network call, or ctx is cancelled. Holding the lock across the wait
// serialises callers so the global rate stays within policy.
func (c *Client) waitRate(ctx context.Context) error {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()
	if wait := c.minInterval - time.Since(c.lastCall); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.lastCall = time.Now()
	return nil
}

func (c *Client) cached(key string) (Result, bool) {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	r, ok := c.cache[key]
	return r, ok
}

func (c *Client) store(key string, r Result) {
	c.cacheMu.Lock()
	c.cache[key] = r
	c.cacheMu.Unlock()
}

func cacheKey(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}
