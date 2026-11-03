// Package google wraps the Google Maps Platform APIs used by the app:
//   - Places API (New) Text Search  – locate stations by name
//   - Geocoding API                 – locate towns / free-text origins
//   - Routes API computeRouteMatrix – road distance & travel time
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"stationfinder/internal/geo"
)

var ErrNotFound = errors.New("no results")

// Endpoints can be overridden (used by tests).
type Endpoints struct {
	PlacesSearchText   string
	PlacesAutocomplete string
	PlaceDetails       string // base URL; the place ID is appended
	Geocode            string
	RouteMatrix        string
}

var DefaultEndpoints = Endpoints{
	PlacesSearchText:   "https://places.googleapis.com/v1/places:searchText",
	PlacesAutocomplete: "https://places.googleapis.com/v1/places:autocomplete",
	PlaceDetails:       "https://places.googleapis.com/v1/places/",
	Geocode:            "https://maps.googleapis.com/maps/api/geocode/json",
	RouteMatrix:        "https://routes.googleapis.com/distanceMatrix/v2:computeRouteMatrix",
}

type Client struct {
	key  string
	http *http.Client
	ep   Endpoints
}

func New(apiKey string) *Client {
	return NewWithEndpoints(apiKey, DefaultEndpoints)
}

func NewWithEndpoints(apiKey string, ep Endpoints) *Client {
	return &Client{key: apiKey, http: &http.Client{Timeout: 30 * time.Second}, ep: ep}
}

func (c *Client) Enabled() bool { return c.key != "" }

// Place is a located point.
type Place struct {
	Location geo.LatLng `json:"location"`
	Name     string     `json:"name,omitempty"`
	Address  string     `json:"address,omitempty"`
	PlaceID  string     `json:"placeId,omitempty"`
}

type apiError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func (c *Client) postJSON(ctx context.Context, endpoint, fieldMask string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodPost, endpoint, fieldMask, bytes.NewReader(buf), out)
}

func (c *Client) doJSON(ctx context.Context, method, endpoint, fieldMask string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Goog-Api-Key", c.key)
	if fieldMask != "" {
		req.Header.Set("X-Goog-FieldMask", fieldMask)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var ae apiError
		if json.Unmarshal(raw, &ae) == nil && ae.Error.Message != "" {
			return fmt.Errorf("google %s: %s (%s)", endpoint, ae.Error.Message, ae.Error.Status)
		}
		return fmt.Errorf("google %s: HTTP %d: %s", endpoint, resp.StatusCode, truncate(string(raw), 300))
	}
	return json.Unmarshal(raw, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// PlacesTextSearch returns candidate places in Sri Lanka for the query (best first).
func (c *Client) PlacesTextSearch(ctx context.Context, query string) ([]Place, error) {
	body := map[string]any{
		"textQuery":    query,
		"regionCode":   "LK",
		"languageCode": "en",
		"locationRestriction": map[string]any{
			"rectangle": map[string]any{
				"low":  map[string]float64{"latitude": 5.85, "longitude": 79.5},
				"high": map[string]float64{"latitude": 9.9, "longitude": 81.95},
			},
		},
	}
	var resp struct {
		Places []struct {
			ID          string `json:"id"`
			DisplayName struct {
				Text string `json:"text"`
			} `json:"displayName"`
			FormattedAddress string `json:"formattedAddress"`
			Location         struct {
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			} `json:"location"`
		} `json:"places"`
	}
	err := c.postJSON(ctx, c.ep.PlacesSearchText,
		"places.id,places.displayName,places.formattedAddress,places.location", body, &resp)
	if err != nil {
		return nil, err
	}
	if len(resp.Places) == 0 {
		return nil, ErrNotFound
	}
	out := make([]Place, 0, len(resp.Places))
	for _, p := range resp.Places {
		out = append(out, Place{
			Location: geo.LatLng{Lat: p.Location.Latitude, Lng: p.Location.Longitude},
			Name:     p.DisplayName.Text,
			Address:  p.FormattedAddress,
			PlaceID:  p.ID,
		})
	}
	return out, nil
}

// Suggestion is one Places Autocomplete prediction.
type Suggestion struct {
	PlaceID       string `json:"placeId"`
	MainText      string `json:"mainText"`
	SecondaryText string `json:"secondaryText,omitempty"`
}

// Autocomplete returns place predictions in Sri Lanka for partially typed input.
// sessionToken groups the keystrokes and the final PlaceDetails call into one billed session.
func (c *Client) Autocomplete(ctx context.Context, input, sessionToken string) ([]Suggestion, error) {
	body := map[string]any{
		"input":               input,
		"includedRegionCodes": []string{"lk"},
		"languageCode":        "en",
	}
	if sessionToken != "" {
		body["sessionToken"] = sessionToken
	}
	var resp struct {
		Suggestions []struct {
			PlacePrediction *struct {
				PlaceID string `json:"placeId"`
				Text    struct {
					Text string `json:"text"`
				} `json:"text"`
				StructuredFormat struct {
					MainText struct {
						Text string `json:"text"`
					} `json:"mainText"`
					SecondaryText struct {
						Text string `json:"text"`
					} `json:"secondaryText"`
				} `json:"structuredFormat"`
			} `json:"placePrediction"`
		} `json:"suggestions"`
	}
	if err := c.postJSON(ctx, c.ep.PlacesAutocomplete, "", body, &resp); err != nil {
		return nil, err
	}
	out := make([]Suggestion, 0, len(resp.Suggestions))
	for _, s := range resp.Suggestions {
		p := s.PlacePrediction
		if p == nil || p.PlaceID == "" {
			continue
		}
		main := p.StructuredFormat.MainText.Text
		if main == "" {
			main = p.Text.Text
		}
		out = append(out, Suggestion{PlaceID: p.PlaceID, MainText: main, SecondaryText: p.StructuredFormat.SecondaryText.Text})
	}
	return out, nil
}

// PlaceDetails returns the location of a place ID (e.g. from Autocomplete).
func (c *Client) PlaceDetails(ctx context.Context, placeID, sessionToken string) (*Place, error) {
	q := url.Values{}
	q.Set("languageCode", "en")
	if sessionToken != "" {
		q.Set("sessionToken", sessionToken)
	}
	var resp struct {
		ID          string `json:"id"`
		DisplayName struct {
			Text string `json:"text"`
		} `json:"displayName"`
		FormattedAddress string `json:"formattedAddress"`
		Location         *struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"location"`
	}
	endpoint := c.ep.PlaceDetails + url.PathEscape(placeID) + "?" + q.Encode()
	if err := c.doJSON(ctx, http.MethodGet, endpoint, "id,displayName,formattedAddress,location", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Location == nil {
		return nil, ErrNotFound
	}
	return &Place{
		Location: geo.LatLng{Lat: resp.Location.Latitude, Lng: resp.Location.Longitude},
		Name:     resp.DisplayName.Text,
		Address:  resp.FormattedAddress,
		PlaceID:  resp.ID,
	}, nil
}

// Geocode resolves an address restricted to Sri Lanka.
func (c *Client) Geocode(ctx context.Context, address string) (*Place, error) {
	q := url.Values{}
	q.Set("address", address)
	q.Set("components", "country:LK")
	q.Set("region", "lk")
	q.Set("key", c.key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.ep.Geocode+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Don't leak the key: url.Error includes the full request URL.
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("geocoding request failed: %w", ue.Err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Status       string `json:"status"`
		ErrorMessage string `json:"error_message"`
		Results      []struct {
			FormattedAddress string   `json:"formatted_address"`
			PlaceID          string   `json:"place_id"`
			Types            []string `json:"types"`
			Geometry         struct {
				Location struct {
					Lat float64 `json:"lat"`
					Lng float64 `json:"lng"`
				} `json:"location"`
			} `json:"geometry"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	switch body.Status {
	case "OK":
	case "ZERO_RESULTS":
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("geocoding: %s %s", body.Status, body.ErrorMessage)
	}
	r := body.Results[0]
	// A bare "country" result means Google could not find the place itself.
	if len(r.Types) == 1 && r.Types[0] == "country" {
		return nil, ErrNotFound
	}
	return &Place{
		Location: geo.LatLng{Lat: r.Geometry.Location.Lat, Lng: r.Geometry.Location.Lng},
		Address:  r.FormattedAddress,
		PlaceID:  r.PlaceID,
	}, nil
}

// Route is one origin→destination result of the route matrix.
type Route struct {
	OK          bool
	DistanceKm  float64
	DurationMin float64
}

const matrixBatch = 100

// RouteMatrix computes road distance & duration from origin to each destination.
// The returned slice is index-aligned with dests.
func (c *Client) RouteMatrix(ctx context.Context, origin geo.LatLng, dests []geo.LatLng, travelMode string) ([]Route, error) {
	out := make([]Route, len(dests))
	for start := 0; start < len(dests); start += matrixBatch {
		end := min(start+matrixBatch, len(dests))
		if err := c.routeMatrixBatch(ctx, origin, dests[start:end], travelMode, out[start:end]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func waypoint(p geo.LatLng) map[string]any {
	return map[string]any{
		"waypoint": map[string]any{
			"location": map[string]any{
				"latLng": map[string]float64{"latitude": p.Lat, "longitude": p.Lng},
			},
		},
	}
}

func (c *Client) routeMatrixBatch(ctx context.Context, origin geo.LatLng, dests []geo.LatLng, travelMode string, out []Route) error {
	destinations := make([]any, len(dests))
	for i, d := range dests {
		destinations[i] = waypoint(d)
	}
	body := map[string]any{
		"origins":           []any{waypoint(origin)},
		"destinations":      destinations,
		"travelMode":        travelMode,
		"routingPreference": "TRAFFIC_UNAWARE",
	}
	if travelMode != "DRIVE" && travelMode != "TWO_WHEELER" {
		delete(body, "routingPreference")
	}
	var elems []struct {
		DestinationIndex int    `json:"destinationIndex"`
		DistanceMeters   int    `json:"distanceMeters"`
		Duration         string `json:"duration"`
		Condition        string `json:"condition"`
		Status           struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"status"`
	}
	err := c.postJSON(ctx, c.ep.RouteMatrix,
		"originIndex,destinationIndex,distanceMeters,duration,condition,status", body, &elems)
	if err != nil {
		return err
	}
	for _, e := range elems {
		if e.DestinationIndex < 0 || e.DestinationIndex >= len(out) {
			continue
		}
		if e.Status.Code != 0 || e.Condition != "ROUTE_EXISTS" {
			continue
		}
		secs, _ := strconv.ParseFloat(strings.TrimSuffix(e.Duration, "s"), 64)
		out[e.DestinationIndex] = Route{
			OK:          true,
			DistanceKm:  float64(e.DistanceMeters) / 1000,
			DurationMin: secs / 60,
		}
	}
	return nil
}
