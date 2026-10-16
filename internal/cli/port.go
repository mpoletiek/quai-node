package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mpoletiek/quai-node/internal/upnp"
)

// routerMappings lists the router's port mappings in the background:
// receive from the channel once the answer is needed. A network without a
// UPnP router gives none.
func routerMappings() <-chan []upnp.Mapping {
	ch := make(chan []upnp.Mapping, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		ms, _ := upnp.Mappings(ctx, 2*time.Second)
		ch <- ms
	}()
	return ch
}

// portCheck is what setup knows about a P2P port.
type portCheck struct {
	Conflict *upnp.Conflict // nil: nothing else uses the port
	Suggest  int            // a free port instead, 0 if none was found
}

// checkPort looks for another machine (per the router) or another program
// on this one using port. held is the port this install's own node may be
// listening on already, which doesn't count as taken here.
func checkPort(port, held int, ms []upnp.Mapping) portCheck {
	c := upnp.Check(port, ms, upnp.LocalAddrs())
	if c != nil {
		c.Name = upnp.Name(context.Background(), c.Client)
	} else if port != held && !upnp.Free(port) {
		c = &upnp.Conflict{Port: port, Local: true}
	}
	if c == nil {
		return portCheck{}
	}
	return portCheck{Conflict: c, Suggest: upnp.Suggest(port, ms, upnp.Free)}
}

// warnPort tells the user about a conflict setup won't resolve itself.
func warnPort(out io.Writer, pc portCheck) {
	if pc.Conflict == nil {
		return
	}
	fmt.Fprintf(out, "warning: %s\n", pc.Conflict.Problem())
	if pc.Suggest != 0 {
		fmt.Fprintf(out, "  Give this node another port: quai-node setup --p2p-port %d\n", pc.Suggest)
	}
}
