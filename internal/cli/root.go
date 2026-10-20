// Package cli is quai-node's command line.
package cli

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/mpoletiek/quai-node/internal/bundle"
	"github.com/mpoletiek/quai-node/internal/home"
	"github.com/mpoletiek/quai-node/internal/services"
	"github.com/mpoletiek/quai-node/internal/settings"
)

// Version is quai-node's version, set at build time.
var Version = "dev"

var homeFlag string

// Execute runs the command line.
func Execute() error {
	root := &cobra.Command{
		Use:   "quai-node",
		Short: "Set up and run a Quai Network node",
		Long: `quai-node sets up a Quai Network node (go-quai), and optionally the
quai-dash web dashboard, and runs them as services or in the foreground.

Run without a command, it starts setup the first time and shows the node's
status after that.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := theHome()
			if err != nil {
				return err
			}
			if _, err := os.Stat(h.GoQuai()); !h.Exists() || err != nil {
				return runSetup(cmd, setupOpts{}) // first run, or after uninstall
			}
			return runStatus(cmd)
		},
	}
	root.PersistentFlags().StringVar(&homeFlag, "home", "", "quai-node home directory (default: per OS; env QUAI_NODE_HOME)")
	// quainationCmd: the hidden old name of keepersCmd (the Keepers were on quaination.xyz).
	root.AddCommand(setupCmd(), runCmd(), statusCmd(), serviceCmd(), snapshotCmd(), keepersCmd(), quainationCmd(), logsCmd(), uninstallCmd(), versionCmd())
	return root.Execute()
}

func theHome() (home.Home, error) {
	if homeFlag != "" {
		return home.Home{Dir: homeFlag}, nil
	}
	return home.Default()
}

// loadHome is the home and its settings, for commands that need setup done.
func loadHome() (home.Home, settings.Settings, error) {
	h, err := theHome()
	if err != nil {
		return h, settings.Settings{}, err
	}
	if !h.Exists() {
		return h, settings.Settings{}, fmt.Errorf("no node set up in %s: run `quai-node setup`", h.Dir)
	}
	s, err := settings.Load(h)
	return h, s, err
}

// serviceFor is the quai-node service of a home.
func serviceFor(h home.Home, s settings.Settings) services.Spec {
	return services.Service(h, s.RunAs, services.LogDir(h.Dir))
}

// underSudo reports a Unix quai-node started through sudo, which would set
// the node up in root's home rather than the user's.
func underSudo() bool {
	return runtime.GOOS != "windows" && os.Geteuid() == 0 && os.Getenv("SUDO_USER") != ""
}

func currentUser() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the versions of quai-node and what it carries",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("quai-node %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
			m, err := bundle.Read()
			if errors.Is(err, bundle.ErrNoPayload) {
				fmt.Println("no go-quai bundled (development build)")
				return nil
			} else if err != nil {
				return err
			}
			fmt.Printf("go-quai %s (%s)\n", m.GoQuaiVersion, short(m.GoQuaiCommit))
			for _, p := range m.GoQuaiPatches {
				fmt.Printf("  patched: %s\n", p)
			}
			if m.DashVersion != "" {
				fmt.Printf("quai-dash %s\n", m.DashVersion)
			} else {
				fmt.Println("quai-dash: not available for this platform")
			}
			return nil
		},
	}
}

func short(commit string) string {
	if len(commit) > 10 {
		return commit[:10]
	}
	return commit
}

// errNotOurs means the machine's service of that name belongs to another
// quai-node home. Service names are machine-wide; a home acts only on its
// own service.
type errNotOurs struct {
	name, owner, home string
}

func (e errNotOurs) Error() string {
	return fmt.Sprintf("the %s service on this machine belongs to another quai-node install (it runs %s), not this one (%s); manage it from that install, or `quai-node uninstall` there first",
		e.name, e.owner, e.home)
}

// ownService reports whether the service name is installed, and fails if
// it is installed for another home.
func ownService(mgr services.Manager, name string, h home.Home) (installed bool, err error) {
	installed, ours, err := services.Owns(mgr, name, h)
	if err != nil || !installed || ours {
		return installed, err
	}
	def, _ := mgr.Definition(name)
	return true, errNotOurs{name, services.OwnerOf(def), h.Dir}
}
