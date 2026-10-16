package geo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAndRound(t *testing.T) {
	p, err := ParseLatLon(" 47.3667, 8.5500 ")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.LatLon(); got != "47.4,8.6" {
		t.Errorf("LatLon = %s", got)
	}
	if got := (Place{Lat: -33.8688, Lon: 151.2093}).LatLon(); got != "-33.9,151.2" {
		t.Errorf("LatLon = %s", got)
	}
	for _, bad := range []string{"", "47.3", "a,b", "91,0", "0,181"} {
		if _, err := ParseLatLon(bad); err == nil {
			t.Errorf("ParseLatLon(%q) accepted", bad)
		}
	}
}

func TestFindDB(t *testing.T) {
	d := t.TempDir()
	t.Setenv("XDG_DATA_HOME", d)
	t.Setenv("HOME", t.TempDir())
	if FindDB() != "" && !filepath.IsAbs(FindDB()) {
		t.Fatal("odd result")
	}
	g := filepath.Join(d, "GeoIP")
	os.MkdirAll(g, 0o755)
	os.WriteFile(filepath.Join(g, "dbip-City-lite.mmdb"), nil, 0o644)
	if got := FindDB(); got != filepath.Join(g, "dbip-City-lite.mmdb") && filepath.Dir(got) != "/usr/share/GeoIP" && filepath.Dir(got) != "/var/lib/GeoIP" {
		t.Errorf("FindDB = %q", got)
	}
	os.WriteFile(filepath.Join(g, "GeoLite2-City.mmdb"), nil, 0o644)
	if got := FindDB(); got != filepath.Join(g, "GeoLite2-City.mmdb") && filepath.Dir(got) != "/usr/share/GeoIP" && filepath.Dir(got) != "/var/lib/GeoIP" {
		t.Errorf("FindDB = %q, want GeoLite2 preferred", got)
	}
}
