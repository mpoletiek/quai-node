package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mpoletiek/quai-node/internal/services"
	"github.com/mpoletiek/quai-node/internal/settings"
)

func serviceCmd() *cobra.Command {
	var dryRun bool
	var initKind string
	cmd := &cobra.Command{
		Use:   "service install|uninstall|start|stop|restart|status",
		Short: "Manage the quai-node service (the node and the dashboard together)",
		Long: `Manage the quai-node service, which runs go-quai and, when set up,
quai-dash: one start or stop covers both.

Linux: a systemd or OpenRC system service (sudo is used when needed).
macOS: a LaunchAgent of your user. Windows: a service (needs an
elevated terminal).`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"install", "uninstall", "start", "stop", "restart", "status"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if underSudo() {
				return fmt.Errorf("run quai-node as your own user; it uses sudo itself where needed")
			}
			h, s, err := loadHome()
			if err != nil {
				return err
			}
			mgr, err := manager(services.NewRunner(out, dryRun), initKind)
			if err != nil {
				return err
			}
			installed, err := ownService(mgr, services.Name, h)
			if err != nil {
				return err
			}
			if !installed && args[0] != "install" && args[0] != "status" {
				return fmt.Errorf("the %s service isn't installed: `quai-node service install`, or `quai-node run` to run in the foreground", services.Name)
			}
			switch args[0] {
			case "install":
				if err := mgr.Install(serviceFor(h, s)); err != nil {
					return err
				}
				if !dryRun && !s.Service {
					s.Service = true
					return settings.Save(h, s)
				}
			case "uninstall":
				if err := mgr.Uninstall(services.Name); err != nil {
					return err
				}
				if !dryRun && s.Service {
					s.Service = false
					return settings.Save(h, s)
				}
			case "start":
				return mgr.Start(services.Name)
			case "stop":
				return mgr.Stop(services.Name)
			case "restart":
				if err := mgr.Stop(services.Name); err != nil {
					return err
				}
				return mgr.Start(services.Name)
			case "status":
				st, err := mgr.Status(services.Name)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "%s %s service: %s\n", services.Name, mgr.Kind(), st)
				fmt.Fprintln(out, services.LogNote(mgr.Kind(), h))
			default:
				return fmt.Errorf("unknown action %q", args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the files and commands, change nothing")
	cmd.Flags().StringVar(&initKind, "init", "", "service manager (Linux: systemd or openrc; default: detected)")
	return cmd
}

func logsCmd() *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the end of the node's zone log",
		RunE: func(cmd *cobra.Command, args []string) error {
			h, s, err := loadHome()
			if err != nil {
				return err
			}
			z := s.ZoneOf()
			path := filepath.Join(h.NodeLogs(), fmt.Sprintf("zone-%d-%d.log", z.Region, z.Zone))
			b, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("%w (has the node run yet?)", err)
			}
			all := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			if len(all) > lines {
				all = all[len(all)-lines:]
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(all, "\n"))
			fmt.Fprintf(cmd.OutOrStdout(), "\n(%s; the other chains' logs are beside it)\n", path)
			return nil
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 40, "lines to show")
	return cmd
}
