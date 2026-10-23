package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mpoletiek/quai-node/internal/services"
	"github.com/mpoletiek/quai-node/internal/supervise"
)

func runCmd() *cobra.Command {
	var asService, noDash bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the node (and dashboard) in the foreground until Ctrl-C",
		Long: `Run go-quai, and quai-dash when set up, in this terminal until Ctrl-C.
go-quai's output shows here; quai-dash's goes to logs/quai-dash.log in
the home directory. If quai-dash stops it is restarted; if go-quai
stops, so does everything.

For when the node isn't a service. (The quai-node service runs
'quai-node run --service'; that flag isn't for use by hand.)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, s, err := loadHome()
			if err != nil {
				return err
			}
			children := services.Children(h, s.Dashboard && !noDash, h.Logs())
			if asService {
				return runService(h, children, claimer(h, s))
			}
			if mgr, err := services.Detect(services.NewRunner(cmd.OutOrStdout(), true)); err == nil {
				if st, _ := mgr.Status(services.Name); st == services.Running {
					return errors.New("the node already runs as a service; see `quai-node status`, or `quai-node service stop` first")
				}
			}
			if err := ensureBundle(h); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if len(children) > 1 {
				fmt.Printf("quai-node: dashboard at http://%s (its log: %s)\n", s.DashboardListen, children[1].LogFile)
			}
			fmt.Println("quai-node: Ctrl-C stops the node; it may take a minute to close its databases")
			return supervise.Run(ctx, children, supervise.Options{Out: os.Stdout, Alongside: claimer(h, s)})
		},
	}
	cmd.Flags().BoolVar(&asService, "service", false, "run under the service manager")
	cmd.Flags().BoolVar(&noDash, "no-dashboard", false, "run the node only")
	_ = cmd.Flags().MarkHidden("service")
	return cmd
}
