package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/mpoletiek/quai-node/internal/services"
	"github.com/mpoletiek/quai-node/internal/status"
)

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the node runs, its height, sync progress and peers",
		RunE:  func(cmd *cobra.Command, args []string) error { return runStatus(cmd) },
	}
}

func runStatus(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	h, s, err := loadHome()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	n := status.Read(ctx, s.ZoneRPC(), status.PublicRPC(s.Network, s.ZoneOf().Name()))

	fmt.Fprintf(out, "quai-node   %s · %s   (%s)\n", s.Network, s.ZoneOf().Name(), h.Dir)

	if s.Service {
		line := "Service     quai-node: "
		if mgr, err := services.Detect(services.NewRunner(io.Discard, true)); err == nil {
			if _, err := ownService(mgr, services.Name, h); err != nil {
				line += err.Error()
			} else {
				st, _ := mgr.Status(services.Name)
				line += fmt.Sprintf("%s (%s)", st, mgr.Kind())
			}
		} else {
			line += err.Error()
		}
		fmt.Fprintln(out, line)
	}
	if n.Reachable {
		fmt.Fprintf(out, "Node        answering on %s\n", s.ZoneRPC())
		line := fmt.Sprintf("Height      %s", humanize.Comma(int64(n.Height)))
		if p := n.Progress(); p >= 0 {
			line += fmt.Sprintf(" of %s  (%.1f%%)", humanize.Comma(int64(n.Network)), p*100)
			if n.Height+3 >= n.Network {
				line += "  synced"
			} else {
				line += "  syncing"
			}
		}
		fmt.Fprintln(out, line)
		fmt.Fprintf(out, "Peers       %d\n", n.Peers)
	} else {
		fmt.Fprintf(out, "Node        not answering on %s\n", s.ZoneRPC())
		if s.Service {
			fmt.Fprintln(out, "            start it with `quai-node service start`")
		} else {
			fmt.Fprintln(out, "            start it with `quai-node run`")
		}
	}
	if s.Dashboard {
		state := "not answering"
		if status.Answers(ctx, "http://127.0.0.1:"+s.DashboardPort()+"/") {
			state = "answering"
		}
		fmt.Fprintf(out, "Dashboard   http://%s  (%s)\n", s.DashboardListen, state)
	}
	if s.Mining {
		fmt.Fprintln(out, "Mining      stratum on ports 3333 (KawPoW), 3334 (Scrypt), 3335 (SHA-256)")
	}
	if s.Reporting() {
		_ = quainationStatus(ctx, out, s, false)
	}
	kind := ""
	if s.Service {
		if mgr, err := services.Detect(services.NewRunner(io.Discard, true)); err == nil {
			kind = mgr.Kind()
		}
	}
	fmt.Fprintf(out, "Logs\n%s\n", services.LogNote(kind, h))
	return nil
}
