package upnp

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Conflict is another machine on the network, or another program on this
// one, using a P2P port.
type Conflict struct {
	Port    int
	Local   bool   // taken on this machine
	Client  string // the other machine's LAN address
	Name    string // its host name, when reverse DNS knows it
	Mapping Mapping
}

// Who names the other machine: "machine1 (10.0.0.12)", or the address.
func (c Conflict) Who() string {
	if c.Name != "" {
		return c.Name + " (" + c.Client + ")"
	}
	return c.Client
}

// Problem says what is wrong, in a sentence or two.
func (c Conflict) Problem() string {
	if c.Local {
		return fmt.Sprintf("P2P port %d is already in use by another program on this machine.", c.Port)
	}
	return fmt.Sprintf("P2P port %d is already used by %s on your network: your router forwards it there. "+
		"Two Quai nodes behind one router can't share a P2P port; the router mixes up their connections and one of them finds no peers.",
		c.Port, c.Who())
}

// Check reports the first mapping that sends port, inside or outside the
// router, to a machine other than this one, or nil.
func Check(port int, ms []Mapping, local map[string]bool) *Conflict {
	for _, m := range ms {
		if !m.Enabled || local[m.Client] {
			continue
		}
		if m.InternalPort == port || m.ExternalPort == port {
			return &Conflict{Port: port, Client: m.Client, Mapping: m}
		}
	}
	return nil
}

// Suggest is the first port after port that no mapping uses, at either
// end, and that free says is free on this machine; 0 if none is.
func Suggest(port int, ms []Mapping, free func(int) bool) int {
	used := map[int]bool{}
	for _, m := range ms {
		used[m.InternalPort], used[m.ExternalPort] = true, true
	}
	for p := port + 1; p < port+200 && p <= 65535; p++ {
		if !used[p] && free(p) {
			return p
		}
	}
	return 0
}

// LocalAddrs are this machine's interface addresses.
func LocalAddrs() map[string]bool {
	out := map[string]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			out[n.IP.String()] = true
		}
	}
	return out
}

// Free reports whether nothing on this machine listens on port, TCP or UDP.
func Free(port int) bool {
	addr := ":" + strconv.Itoa(port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	l.Close()
	c, err := net.ListenPacket("udp", addr)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// Name looks up addr's host name, "" when unknown.
func Name(ctx context.Context, addr string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, addr)
	if err != nil || len(names) == 0 {
		return ""
	}
	n := strings.TrimSuffix(names[0], ".")
	if short, _, ok := strings.Cut(n, "."); ok && short != "" {
		return short
	}
	return n
}
