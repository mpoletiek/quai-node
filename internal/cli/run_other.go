//go:build !windows

package cli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/mpoletiek/quai-node/internal/home"
	"github.com/mpoletiek/quai-node/internal/services"
	"github.com/mpoletiek/quai-node/internal/supervise"
)

// runService runs the children for systemd, OpenRC or launchd, which
// stop it with SIGINT or SIGTERM and keep its output.
func runService(_ home.Home, children []services.Spec, also func(context.Context, io.Writer)) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return supervise.Run(ctx, children, supervise.Options{Out: os.Stdout, Service: true, Alongside: also})
}
