// Package geoip turns a visitor's IP address into a location with a MaxMind database
// (GeoLite2-City, free with a MaxMind account, or the paid GeoIP2-City), read from a .mmdb file.
//
// Locations become profile attributes (geo_country, geo_country_code, geo_region, geo_city,
// geo_postal, geo_timezone), so segments can filter on them and quiet hours fall back to the
// visitor's time zone. Without a database, nothing is looked up.
package geoip

import (
	"net"
	"strings"
	"sync"

	"github.com/oschwald/geoip2-golang"
)

// Location is what a lookup found; empty fields were unknown.
type Location struct {
	Country     string
	CountryCode string
	Region      string // state or province
	City        string
	Postal      string
	TimeZone    string
}

// Attrs returns the location as profile attributes (only the known ones).
func (l Location) Attrs() map[string]any {
	out := map[string]any{}
	set := func(k, v string) {
		if v != "" {
			out[k] = v
		}
	}
	set("geo_country", l.Country)
	set("geo_country_code", l.CountryCode)
	set("geo_region", l.Region)
	set("geo_city", l.City)
	set("geo_postal", l.Postal)
	set("geo_timezone", l.TimeZone)
	return out
}

// Locator looks up IP addresses. A nil *Locator finds nothing.
type Locator struct {
	mu sync.RWMutex
	db *geoip2.Reader
}

// Open loads a .mmdb file.
func Open(path string) (*Locator, error) {
	db, err := geoip2.Open(path)
	if err != nil {
		return nil, err
	}
	return &Locator{db: db}, nil
}

// Lookup finds ip's location. Private and malformed addresses find nothing.
func (l *Locator) Lookup(ip string) (Location, bool) {
	if l == nil {
		return Location{}, false
	}
	addr := net.ParseIP(strings.TrimSpace(ip))
	if addr == nil || addr.IsPrivate() || addr.IsLoopback() || addr.IsUnspecified() {
		return Location{}, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	rec, err := l.db.City(addr)
	if err != nil {
		return Location{}, false
	}
	loc := Location{Country: rec.Country.Names["en"], CountryCode: rec.Country.IsoCode, City: rec.City.Names["en"],
		Postal: rec.Postal.Code, TimeZone: rec.Location.TimeZone}
	if len(rec.Subdivisions) > 0 {
		loc.Region = rec.Subdivisions[0].Names["en"]
	}
	return loc, loc.CountryCode != ""
}

func (l *Locator) Close() error {
	if l == nil {
		return nil
	}
	return l.db.Close()
}
