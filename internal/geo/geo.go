// Package geo helps setup place the node and its peers on quai-dash's map:
// it finds a local GeoIP database where quai-dash looks for one, and looks
// up this machine's own location from its public IP.
package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Peer location sources, as quai-dash's geoip setting names them.
const (
	Online = "online" // ip-api.com, which receives the peers' addresses
	DB     = "db"     // a local MaxMind City database
	Off    = "off"
	Auto   = "auto" // quai-dash's default: a local database if found, else off
)

// DBDirs are where quai-dash looks for a City database (geo::db_dirs).
func DBDirs() []string {
	dirs := []string{"/usr/share/GeoIP", "/var/lib/GeoIP"}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, "GeoIP"))
	}
	if h, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(h, ".local", "share", "GeoIP"))
	}
	return dirs
}

// FindDB is the database quai-dash would pick (geo::find_db), or "".
func FindDB() string {
	for _, d := range DBDirs() {
		for _, name := range []string{"GeoLite2-City.mmdb", "GeoIP2-City.mmdb"} {
			p := filepath.Join(d, name)
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
				return p
			}
		}
		matches, _ := filepath.Glob(filepath.Join(d, "*City*.mmdb"))
		sort.Strings(matches)
		if len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

// Place is a position on the map.
type Place struct {
	Lat, Lon float64
	City     string // "Zürich, CH"; empty when unknown
	Source   string // the service that answered
}

// LatLon is the position as quai-dash's here setting takes it, rounded to
// a tenth of a degree (about 10 km): plenty for a world map, and no more
// precise than it needs to be.
func (p Place) LatLon() string {
	r := func(f float64) string { return strconv.FormatFloat(math.Round(f*10)/10, 'f', 1, 64) }
	return r(p.Lat) + "," + r(p.Lon)
}

// ParseLatLon reads "LAT,LON".
func ParseLatLon(s string) (Place, error) {
	a, b, ok := strings.Cut(s, ",")
	if !ok {
		return Place{}, errors.New("expected LAT,LON, e.g. 47.4,8.5")
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil {
		return Place{}, errors.New("expected LAT,LON as numbers, e.g. 47.4,8.5")
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return Place{}, errors.New("latitude is -90 to 90, longitude -180 to 180")
	}
	return Place{Lat: lat, Lon: lon}, nil
}

var client = &http.Client{Timeout: 6 * time.Second}

// Detect looks up this machine's location from its public IP: ipinfo.io
// over HTTPS, else ip-api.com (plain HTTP on its free tier). Behind a VPN
// it finds the VPN's exit.
func Detect(ctx context.Context) (Place, error) {
	p, err := ipinfo(ctx)
	if err == nil {
		return p, nil
	}
	p, err2 := ipAPI(ctx)
	if err2 == nil {
		return p, nil
	}
	return Place{}, fmt.Errorf("ipinfo.io: %v; ip-api.com: %v", err, err2)
}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func ipinfo(ctx context.Context) (Place, error) {
	var r struct{ City, Country, Loc string }
	if err := getJSON(ctx, "https://ipinfo.io/json", &r); err != nil {
		return Place{}, err
	}
	p, err := ParseLatLon(r.Loc)
	if err != nil {
		return Place{}, fmt.Errorf("unexpected loc %q", r.Loc)
	}
	p.City, p.Source = join(r.City, r.Country), "ipinfo.io"
	return p, nil
}

func ipAPI(ctx context.Context) (Place, error) {
	var r struct {
		Status, City, CountryCode, Message string
		Lat, Lon                           float64
	}
	if err := getJSON(ctx, "http://ip-api.com/json/?fields=status,message,city,countryCode,lat,lon", &r); err != nil {
		return Place{}, err
	}
	if r.Status != "success" {
		return Place{}, fmt.Errorf("%s", r.Message)
	}
	return Place{Lat: r.Lat, Lon: r.Lon, City: join(r.City, r.CountryCode), Source: "ip-api.com"}, nil
}

func join(city, country string) string {
	switch {
	case city != "" && country != "":
		return city + ", " + country
	case city != "":
		return city
	}
	return country
}
