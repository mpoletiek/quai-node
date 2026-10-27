package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/mpoletiek/quai-node/internal/onpath"
	"github.com/mpoletiek/quai-node/internal/services"
)

func uninstallCmd() *cobra.Command {
	var purge, yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the services and binaries; --purge also removes the chain data and keys",
		Long: `Stop and remove the services, and remove the binaries and configs from
the home directory. The chain data and the node's key (config/private.key)
stay unless --purge, which deletes the whole home directory and the
chain data directory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if underSudo() {
				return errors.New("run quai-node as your own user; it uses sudo itself where needed")
			}
			h, s, err := loadHome()
			if err != nil {
				return err
			}
			var doomed []string
			if purge {
				doomed = []string{h.Dir, s.DataDir}
			} else {
				// quai-node.toml stays: it records where the chain data
				// is, for a later setup or uninstall --purge.
				doomed = []string{h.BinDir(), h.DashDir(), h.RunDir(), h.BundleStamp(),
					filepath.Join(h.Dir, "VERSION"), filepath.Join(h.Dir, "params")}
			}
			fmt.Fprintln(out, "This removes the quai-node service, if installed, and:")
			for _, d := range doomed {
				fmt.Fprintf(out, "  %s\n", d)
			}
			if !purge {
				fmt.Fprintf(out, "It keeps the chain data (%s), the node key, logs and quai-node.toml;\n`quai-node uninstall --purge` removes those too.\n", s.DataDir)
			}
			if !yes && !dryRun {
				ok := false
				if err := huh.NewConfirm().Title("Go ahead?").Value(&ok).Run(); err != nil || !ok {
					return errors.New("nothing removed")
				}
			}

			if mgr, err := services.Detect(services.NewRunner(out, dryRun)); err == nil {
				// Only this home's: service names are machine-wide.
				for _, name := range append([]string{services.Name}, services.Legacy...) {
					installed, ours, err := services.Owns(mgr, name, h)
					if err != nil {
						return err
					}
					if !installed {
						continue
					}
					if !ours {
						fmt.Fprintf(out, "Leaving the %s service: it belongs to another quai-node install\n", name)
						continue
					}
					if err := mgr.Uninstall(name); err != nil {
						return err
					}
				}
			}
			if dryRun {
				return nil
			}
			for _, b := range []string{h.QuaiNode(), h.QuaiDash()} {
				if err := onpath.Remove(b); err != nil {
					fmt.Fprintf(out, "%s is still on your PATH: %v\n", filepath.Base(b), err)
				}
			}
			_ = onpath.UnlinkDashConfig(h.DashConfig())
			for _, d := range doomed {
				if err := removeSafely(d); err != nil {
					return err
				}
			}
			fmt.Fprintln(out, "Removed.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete the chain data, node key, configs and logs")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be removed, change nothing")
	return cmd
}

// removeSafely refuses paths that are too short to be ours, so a mangled
// setting can't turn --purge into deleting a home or root directory.
func removeSafely(p string) error {
	clean := filepath.Clean(p)
	userHome, _ := os.UserHomeDir()
	if clean == filepath.VolumeName(clean)+string(filepath.Separator) || clean == filepath.Clean(userHome) ||
		len(strings.Split(filepath.ToSlash(clean), "/")) < 3 {
		return fmt.Errorf("refusing to remove %s; delete it yourself if you mean to", clean)
	}
	return os.RemoveAll(clean)
}
