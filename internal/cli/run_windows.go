//go:build windows

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kardianos/service"

	"github.com/mpoletiek/quai-node/internal/home"
	"github.com/mpoletiek/quai-node/internal/services"
	"github.com/mpoletiek/quai-node/internal/supervise"
)

// program runs the children under the SCM.
type program struct {
	children []services.Spec
	out      *os.File
	cancel   context.CancelFunc
	done     chan struct{}
}

func (p *program) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan struct{})
	supervise.Prepare()
	go func() {
		defer close(p.done)
		err := supervise.Run(ctx, p.children, supervise.Options{Out: p.out, Service: true})
		if err != nil && ctx.Err() == nil {
			// The node stopped on its own: exit non-zero so the SCM's
			// restart-on-failure starts the service again.
			fmt.Fprintln(p.out, err)
			os.Exit(1)
		}
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	p.cancel()
	<-p.done
	return nil
}

// runService runs the children as the Windows service. The SCM keeps no
// output, so it goes to logs\quai-node.log.
func runService(h home.Home, children []services.Spec) error {
	sp := services.Service(h, "", services.LogDir(h.Dir))
	if err := os.MkdirAll(filepath.Dir(sp.LogFile), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(sp.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	svc, err := service.New(&program{children: children, out: out}, services.Config(sp))
	if err != nil {
		return err
	}
	return svc.Run()
}
