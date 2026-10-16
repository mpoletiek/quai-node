// Package upnp reads the router's port mappings over UPnP IGD, so setup
// can spot another machine on the network already using the P2P port.
//
// Two Quai nodes behind one router can't share a P2P port: go-quai dials
// out from its listening port, and a router that keeps source ports makes
// both nodes the same address to a peer, so it drops one node's
// connections and that node finds no peers.
package upnp

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Mapping is one of the router's port forwards.
type Mapping struct {
	Protocol     string // TCP or UDP
	ExternalPort int
	Client       string // the LAN address it forwards to
	InternalPort int
	Description  string
	Enabled      bool
}

// maxEntries bounds the listing, against a router that never says stop.
const maxEntries = 256

var client = &http.Client{Timeout: 5 * time.Second}

// Mappings finds the router (an SSDP search lasting at most wait) and
// lists its port mappings.
func Mappings(ctx context.Context, wait time.Duration) ([]Mapping, error) {
	loc, err := discover(ctx, wait)
	if err != nil {
		return nil, err
	}
	ctl, svc, err := control(ctx, loc)
	if err != nil {
		return nil, err
	}
	return list(ctx, ctl, svc)
}

var gatewayTypes = []string{
	"urn:schemas-upnp-org:device:InternetGatewayDevice:1",
	"urn:schemas-upnp-org:device:InternetGatewayDevice:2",
}

// discover returns the description URL of the first gateway to answer.
func discover(ctx context.Context, wait time.Duration) (string, error) {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	ssdp := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	for _, st := range gatewayTypes {
		msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + st + "\r\n\r\n"
		if _, err := conn.WriteTo([]byte(msg), ssdp); err != nil {
			return "", err
		}
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetReadDeadline(deadline)
	buf := make([]byte, 4096)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return "", errors.New("no UPnP router answered")
		}
		if loc := location(buf[:n]); loc != "" {
			return loc, nil
		}
	}
}

var locationRe = regexp.MustCompile(`(?im)^location:\s*(\S+)`)

func location(reply []byte) string {
	if m := locationRe.FindSubmatch(reply); m != nil {
		return string(m[1])
	}
	return ""
}

type device struct {
	Services []struct {
		Type    string `xml:"serviceType"`
		Control string `xml:"controlURL"`
	} `xml:"serviceList>service"`
	Devices []device `xml:"deviceList>device"`
}

var connectionTypes = []string{
	"urn:schemas-upnp-org:service:WANIPConnection:2",
	"urn:schemas-upnp-org:service:WANIPConnection:1",
	"urn:schemas-upnp-org:service:WANPPPConnection:1",
}

// control reads the gateway's description for its WAN connection service.
func control(ctx context.Context, loc string) (ctl, svc string, err error) {
	body, err := get(ctx, loc)
	if err != nil {
		return "", "", err
	}
	return findControl(loc, body)
}

func findControl(loc string, body []byte) (ctl, svc string, err error) {
	var root struct {
		Device device `xml:"device"`
	}
	if err := xml.Unmarshal(body, &root); err != nil {
		return "", "", fmt.Errorf("router description: %w", err)
	}
	found := map[string]string{}
	var walk func(d device)
	walk = func(d device) {
		for _, s := range d.Services {
			found[strings.TrimSpace(s.Type)] = strings.TrimSpace(s.Control)
		}
		for _, c := range d.Devices {
			walk(c)
		}
	}
	walk(root.Device)
	base, err := url.Parse(loc)
	if err != nil {
		return "", "", err
	}
	for _, t := range connectionTypes {
		if c, ok := found[t]; ok && c != "" {
			u, err := base.Parse(c)
			if err != nil {
				return "", "", err
			}
			return u.String(), t, nil
		}
	}
	return "", "", errors.New("the router offers no WAN connection service")
}

// list asks for mapping after mapping until the router says there are no
// more (a SOAP fault).
func list(ctx context.Context, ctl, svc string) ([]Mapping, error) {
	var out []Mapping
	for i := 0; i < maxEntries; i++ {
		body, err := soap(ctx, ctl, svc, "GetGenericPortMappingEntry",
			"<NewPortMappingIndex>"+strconv.Itoa(i)+"</NewPortMappingIndex>")
		if err != nil {
			if errors.Is(err, errFault) && i > 0 {
				break
			}
			if errors.Is(err, errFault) {
				return nil, nil // none, or the router won't list them
			}
			return out, err
		}
		m, err := parseEntry(body)
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, nil
}

var errFault = errors.New("UPnP fault")

func soap(ctx context.Context, ctl, svc, action, args string) ([]byte, error) {
	env := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:` + action + ` xmlns:u="` + svc + `">` +
		args + `</u:` + action + `></s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ctl, strings.NewReader(env))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+svc+`#`+action+`"`)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		if bytes.Contains(body, []byte("Fault")) {
			return nil, errFault
		}
		return nil, fmt.Errorf("router: HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func parseEntry(body []byte) (Mapping, error) {
	var env struct {
		Body struct {
			Resp struct {
				Protocol     string `xml:"NewProtocol"`
				ExternalPort string `xml:"NewExternalPort"`
				Client       string `xml:"NewInternalClient"`
				InternalPort string `xml:"NewInternalPort"`
				Description  string `xml:"NewPortMappingDescription"`
				Enabled      string `xml:"NewEnabled"`
			} `xml:",any"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(body, &env); err != nil {
		return Mapping{}, fmt.Errorf("router mapping: %w", err)
	}
	r := env.Body.Resp
	ext, _ := strconv.Atoi(strings.TrimSpace(r.ExternalPort))
	in, _ := strconv.Atoi(strings.TrimSpace(r.InternalPort))
	return Mapping{
		Protocol:     strings.ToUpper(strings.TrimSpace(r.Protocol)),
		ExternalPort: ext,
		Client:       strings.TrimSpace(r.Client),
		InternalPort: in,
		Description:  strings.TrimSpace(r.Description),
		Enabled:      strings.TrimSpace(r.Enabled) != "0",
	}, nil
}

func get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("router description: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
