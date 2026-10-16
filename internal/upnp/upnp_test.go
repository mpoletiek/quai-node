package upnp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestLocation(t *testing.T) {
	reply := "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=120\r\nLocation: http://10.0.0.254:1900/igd.xml\r\nST: x\r\n\r\n"
	if got := location([]byte(reply)); got != "http://10.0.0.254:1900/igd.xml" {
		t.Fatalf("location = %q", got)
	}
	if got := location([]byte("HTTP/1.1 200 OK\r\n\r\n")); got != "" {
		t.Fatalf("location = %q, want none", got)
	}
}

const description = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
 <device>
  <deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:1</deviceType>
  <serviceList><service><serviceType>urn:schemas-upnp-org:service:Layer3Forwarding:1</serviceType><controlURL>/l3f</controlURL></service></serviceList>
  <deviceList><device>
   <deviceType>urn:schemas-upnp-org:device:WANDevice:1</deviceType>
   <deviceList><device>
    <deviceType>urn:schemas-upnp-org:device:WANConnectionDevice:1</deviceType>
    <serviceList><service>
     <serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>
     <controlURL>/upnp/control/WANIPConn1</controlURL>
    </service></serviceList>
   </device></deviceList>
  </device></deviceList>
 </device>
</root>`

func TestFindControl(t *testing.T) {
	ctl, svc, err := findControl("http://10.0.0.254:1900/igd.xml", []byte(description))
	if err != nil {
		t.Fatal(err)
	}
	if ctl != "http://10.0.0.254:1900/upnp/control/WANIPConn1" || svc != "urn:schemas-upnp-org:service:WANIPConnection:1" {
		t.Fatalf("got %s %s", ctl, svc)
	}
	if _, _, err := findControl("http://x/", []byte(`<root><device></device></root>`)); err == nil {
		t.Fatal("want an error for a router without a WAN connection")
	}
}

// router serves a description and the given mappings, then the fault
// routers answer an index past the end with.
func router(t *testing.T, ms []Mapping) *httptest.Server {
	idx := regexp.MustCompile(`<NewPortMappingIndex>(\d+)</NewPortMappingIndex>`)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			io.WriteString(w, description)
			return
		}
		if !strings.Contains(r.Header.Get("SOAPAction"), "#GetGenericPortMappingEntry") {
			t.Errorf("SOAPAction %q", r.Header.Get("SOAPAction"))
		}
		body, _ := io.ReadAll(r.Body)
		m := idx.FindSubmatch(body)
		i, _ := strconv.Atoi(string(m[1]))
		if i >= len(ms) {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultstring>UPnPError</faultstring><detail><UPnPError><errorCode>713</errorCode></UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
			return
		}
		e := ms[i]
		fmt.Fprintf(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body>`+
			`<u:GetGenericPortMappingEntryResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">`+
			`<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort><NewProtocol>%s</NewProtocol>`+
			`<NewInternalPort>%d</NewInternalPort><NewInternalClient>%s</NewInternalClient><NewEnabled>1</NewEnabled>`+
			`<NewPortMappingDescription>%s</NewPortMappingDescription><NewLeaseDuration>60</NewLeaseDuration>`+
			`</u:GetGenericPortMappingEntryResponse></s:Body></s:Envelope>`,
			e.ExternalPort, e.Protocol, e.InternalPort, e.Client, e.Description)
	}))
}

func TestList(t *testing.T) {
	want := []Mapping{
		{Protocol: "TCP", ExternalPort: 25254, Client: "10.0.0.11", InternalPort: 4002, Description: "libp2p", Enabled: true},
		{Protocol: "TCP", ExternalPort: 23059, Client: "10.0.0.12", InternalPort: 4002, Description: "libp2p", Enabled: true},
	}
	srv := router(t, want)
	defer srv.Close()
	ctx := context.Background()
	ctl, svc, err := control(ctx, srv.URL+"/igd.xml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := list(ctx, ctl, svc)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}

	empty := router(t, nil)
	defer empty.Close()
	ctl, svc, _ = control(ctx, empty.URL+"/igd.xml")
	if got, err := list(ctx, ctl, svc); err != nil || len(got) != 0 {
		t.Fatalf("no mappings: got %v, %v", got, err)
	}
}

func TestCheck(t *testing.T) {
	ms := []Mapping{
		{Protocol: "TCP", ExternalPort: 25254, Client: "10.0.0.11", InternalPort: 4002, Enabled: true}, // this machine
		{Protocol: "TCP", ExternalPort: 23059, Client: "10.0.0.12", InternalPort: 4002, Enabled: true},
		{Protocol: "TCP", ExternalPort: 4003, Client: "192.168.122.1", InternalPort: 4003, Enabled: true},
		{Protocol: "UDP", ExternalPort: 4010, Client: "10.0.0.13", InternalPort: 5000, Enabled: false},
	}
	local := map[string]bool{"10.0.0.11": true, "127.0.0.1": true}
	c := Check(4002, ms, local)
	if c == nil || c.Client != "10.0.0.12" {
		t.Fatalf("4002: got %+v, want 10.0.0.12", c)
	}
	if c := Check(4003, ms, local); c == nil || c.Client != "192.168.122.1" {
		t.Fatalf("4003 (external and internal): got %+v", c)
	}
	if c := Check(4010, ms, local); c != nil {
		t.Fatalf("4010 is a disabled mapping: got %+v", c)
	}
	if c := Check(4002, ms[:1], local); c != nil {
		t.Fatalf("this machine's own mapping: got %+v", c)
	}
	if c := Check(4002, nil, local); c != nil {
		t.Fatalf("no router: got %+v", c)
	}

	c.Name = "machine1"
	if c.Who() != "machine1 (10.0.0.12)" || !strings.Contains(c.Problem(), "machine1 (10.0.0.12)") {
		t.Fatalf("Who %q, Problem %q", c.Who(), c.Problem())
	}
}

func TestSuggest(t *testing.T) {
	ms := []Mapping{
		{ExternalPort: 23059, InternalPort: 4002},
		{ExternalPort: 4003, InternalPort: 4003},
		{ExternalPort: 9000, InternalPort: 4004},
	}
	busy := map[int]bool{4005: true}
	free := func(p int) bool { return !busy[p] }
	if got := Suggest(4002, ms, free); got != 4006 {
		t.Fatalf("Suggest = %d, want 4006 (4003, 4004 mapped; 4005 busy here)", got)
	}
	if got := Suggest(4002, nil, func(int) bool { return false }); got != 0 {
		t.Fatalf("nothing free: got %d", got)
	}
}
