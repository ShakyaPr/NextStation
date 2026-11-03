// Package geo holds small geographic helpers.
package geo

import "math"

type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// HaversineKm returns the great-circle distance between two points in km.
func HaversineKm(a, b LatLng) float64 {
	const r = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := toRad(b.Lat - a.Lat)
	dLng := toRad(b.Lng - a.Lng)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(a.Lat))*math.Cos(toRad(b.Lat))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

// InSriLanka reports whether the point lies in Sri Lanka's bounding box.
func InSriLanka(p LatLng) bool {
	return p.Lat >= 5.85 && p.Lat <= 9.9 && p.Lng >= 79.5 && p.Lng <= 81.95
}

// DistrictCentres gives approximate centres of the districts / RDHS areas used in the PDF.
var DistrictCentres = map[string]LatLng{
	"AMPARA":       {7.2975, 81.6820},
	"ANURADHAPURA": {8.3114, 80.4037},
	"BADULLA":      {6.9934, 81.0550},
	"BATTICALOA":   {7.7310, 81.6747},
	"COLOMBO":      {6.9271, 79.8612},
	"GALLE":        {6.0535, 80.2210},
	"GAMPAHA":      {7.0873, 79.9990},
	"HAMBANTOTA":   {6.1241, 81.1185},
	"JAFFNA":       {9.6615, 80.0255},
	"KALMUNAI":     {7.4167, 81.8167},
	"KALUTARA":     {6.5854, 79.9607},
	"KANDY":        {7.2906, 80.6337},
	"KEGALLE":      {7.2513, 80.3464},
	"KILINOCHCHI":  {9.3803, 80.3770},
	"KURUNEGALA":   {7.4863, 80.3623},
	"MANNAR":       {8.9810, 79.9044},
	"MATALE":       {7.4675, 80.6234},
	"MATARA":       {5.9549, 80.5550},
	"MONERAGALA":   {6.8728, 81.3507},
	"MULLAITIVU":   {9.2671, 80.8142},
	"NUWARA ELIYA": {6.9497, 80.7891},
	"POLONNARUWA":  {7.9403, 81.0188},
	"PUTTALAM":     {8.0362, 79.8283},
	"RATHNAPURA":   {6.6828, 80.3992},
	"TRINCOMALEE":  {8.5874, 81.2152},
	"VAVUNIYA":     {8.7514, 80.4971},
}
