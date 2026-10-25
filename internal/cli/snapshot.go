package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/dustin/go-humanize"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/mpoletiek/quai-node/internal/home"
	"github.com/mpoletiek/quai-node/internal/quaiaddr"
	"github.com/mpoletiek/quai-node/internal/services"
	"github.com/mpoletiek/quai-node/internal/settings"
	"github.com/mpoletiek/quai-node/internal/snapshot"
	"github.com/mpoletiek/quai-node/internal/status"
)

// snapshotFits reports whether quikshot.dev's snapshots fit these
// settings: they are mainnet cyprus1 chains.
func snapshotFits(s settings.Settings) bool {
	return s.Network == "colosseum" && s.ZoneOf() == (quaiaddr.Zone{})
}

// latestSnapshot fetches quikshot.dev's manifest.
func latestSnapshot() (*snapshot.Manifest, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	m, err := snapshot.Latest(ctx)
	if err != nil {
		return nil, fmt.Errorf("couldn't read quikshot.dev's latest snapshot: %w", err)
	}
	return &m, nil
}

// spaceShort says how much space a node in s.DataDir lacks, given the
// chain's size today (the snapshot's unpacked size), or "" when it fits.
// A restore needs the snapshot's size free; a sync needs what the chain
// already there doesn't cover.
func spaceShort(s settings.Settings, m *snapshot.Manifest) string {
	if m == nil {
		return ""
	}
	free, err := home.FreeBytes(s.DataDir)
	if err != nil {
		return ""
	}
	need := m.Need()
	if !s.RestoreSnapshot {
		need -= home.ChainUse(s.DataDir)
	}
	if int64(free) >= need {
		return ""
	}
	return fmt.Sprintf("%s has %s free; the chain needs about %s more (it is %s today, and grows)",
		s.DataDir, humanize.Bytes(free), humanize.Bytes(uint64(need-int64(free))), humanize.Bytes(uint64(m.ExpandedSizeBytes)))
}

// restoreSnapshot replaces the node's chain data with quikshot.dev's
// latest snapshot and switches it to the Pebble engine. The node must not
// be running: a service of this home's is stopped first and started again
// by the caller.
func restoreSnapshot(out io.Writer, h home.Home, s *settings.Settings, mgr services.Manager) error {
	if !snapshotFits(*s) {
		return errors.New("quikshot.dev's snapshots are for mainnet (colosseum) nodes running cyprus1")
	}
	// The engine switch is saved after the download, with extra.toml merged
	// in: a file Save would refuse is turned away before the chain is touched.
	extra, err := settings.LoadExtra(h)
	if err != nil {
		return err
	}
	after := *s
	after.DBEngine = "pebble"
	if err := settings.CheckExtra(h, extra, after); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	m, err := snapshot.Latest(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("quikshot.dev: %w", err)
	}
	if s.IndexUTXOs && !m.Indexed() {
		return errIndexSnapshot
	}
	if short := spaceShort(*s, &m); short != "" {
		return errors.New(short)
	}
	if err := stopNode(out, h, *s, mgr, "restoring"); err != nil {
		return err
	}
	if err := os.MkdirAll(s.DataDir, 0o755); err != nil {
		return err
	}

	fmt.Fprintf(out, "\nRestoring quikshot.dev's snapshot of %s (zone block %s) into %s\n",
		m.Created.Local().Format("Jan 2 15:04"), humanize.Comma(m.ZoneHeight()), s.DataDir)
	fmt.Fprintf(out, "  %s download, %s unpacked, checked against SHA-256 %s…\n",
		humanize.Bytes(uint64(m.SizeBytes)), humanize.Bytes(uint64(m.ExpandedSizeBytes)), m.SHA256[:16])
	fmt.Fprintln(out, "  Ctrl-C stops it; nothing is replaced until the download has checked out.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tty := false
	if f, ok := out.(*os.File); ok {
		tty = isatty.IsTerminal(f.Fd())
	}
	var lastLine time.Time
	err = snapshot.Restore(ctx, m, s.DataDir, func(p snapshot.Progress) {
		line := progressLine(p)
		if tty {
			fmt.Fprintf(out, "\r  %s\033[K", line)
		} else if time.Since(lastLine) > 30*time.Second {
			fmt.Fprintf(out, "  %s\n", line)
			lastLine = time.Now()
		}
	})
	if tty {
		fmt.Fprintln(out)
	}
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("snapshot restore cancelled; the chain data is as it was")
		}
		return fmt.Errorf("snapshot restore: %w (the chain data is as it was)", err)
	}
	// The chains are Pebble now: switch the engine to match, and only now.
	// An index node's chain is indexed from its first block, as the
	// snapshot is; another node's index stops at the snapshot.
	s.DBEngine = "pebble"
	mark := home.UnmarkIndexed
	if s.IndexUTXOs {
		mark = home.MarkIndexed
	}
	if err := mark(s.DataDir); err != nil {
		return err
	}
	if err := settings.Save(h, *s); err != nil {
		return err
	}
	fmt.Fprintf(out, "  restored; the node syncs the rest from zone block %s\n", humanize.Comma(m.ZoneHeight()))
	return nil
}

// stopNode stops this install's service if it runs, and makes sure no
// node is answering on the zone RPC before the chain data is changed.
func stopNode(out io.Writer, h home.Home, s settings.Settings, mgr services.Manager, doing string) error {
	if mgr != nil {
		if installed, ours, _ := services.Owns(mgr, services.Name, h); installed && ours {
			if st, _ := mgr.Status(services.Name); st == services.Running {
				fmt.Fprintf(out, "Stopping the node before %s\n", doing)
				if err := mgr.Stop(services.Name); err != nil {
					return err
				}
			}
		}
	}
	probe, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	running := status.Read(probe, s.ZoneRPC(), "").Reachable
	cancel()
	if running {
		return fmt.Errorf("a node is answering on %s; stop it (Ctrl-C in its terminal) before %s", s.ZoneRPC(), doing)
	}
	return nil
}

// wipeChain deletes the chain in s.DataDir, so the node syncs from scratch.
func wipeChain(out io.Writer, h home.Home, s settings.Settings, mgr services.Manager) error {
	used := home.ChainUse(s.DataDir)
	if used == 0 {
		return nil
	}
	if err := stopNode(out, h, s, mgr, "deleting the chain"); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nDeleting the chain in %s (%s); the node syncs it again from the network\n", s.DataDir, humanize.Bytes(uint64(used)))
	return home.RemoveChain(s.DataDir)
}

func progressLine(p snapshot.Progress) string {
	el := time.Since(p.Started).Seconds()
	rate := float64(p.Downloaded) / max(el, 1)
	line := fmt.Sprintf("%s of %s (%.1f%%) · %s/s", humanize.Bytes(uint64(p.Downloaded)), humanize.Bytes(uint64(p.Total)),
		100*float64(p.Downloaded)/float64(max(p.Total, 1)), humanize.Bytes(uint64(rate)))
	if rate > 0 && p.Downloaded > 0 {
		left := time.Duration(float64(p.Total-p.Downloaded)/rate) * time.Second
		line += " · about " + left.Round(time.Minute).String() + " left"
	}
	return line
}

func snapshotCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "snapshot [restore]",
		Short: "Show quikshot.dev's latest snapshot, or restore the node from it",
		Long: `quikshot.dev publishes verified snapshots of Quai mainnet's chain data
(prime, region-0, zone-0-0). It is an independent community service, not
affiliated with the Quai Network team.

'quai-node snapshot' shows the latest one and whether it fits here.
'quai-node snapshot restore' stops the node, replaces its chain data
with the snapshot (checked against its SHA-256 before anything is
replaced), switches the node to the Pebble engine and starts it again.`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"restore"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			h, s, err := loadHome()
			if err != nil {
				return err
			}
			m, err := latestSnapshot()
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "quikshot.dev snapshot  %s (%s ago)\n", m.Created.Local().Format("Jan 2 2006 15:04"), m.Age())
			fmt.Fprintf(out, "Heights                prime %s · region-0 %s · zone-0-0 %s\n",
				humanize.Comma(m.BlockHeights["prime"]), humanize.Comma(m.BlockHeights["region-0"]), humanize.Comma(m.ZoneHeight()))
			fmt.Fprintf(out, "Size                   %s download, %s unpacked (%s database)\n",
				humanize.Bytes(uint64(m.SizeBytes)), humanize.Bytes(uint64(m.ExpandedSizeBytes)), m.Database)
			if !m.RestoreTested {
				fmt.Fprintln(out, "Restore tested         no (quikshot.dev hasn't test-restored this one)")
			}
			if free, err := home.FreeBytes(s.DataDir); err == nil {
				fmt.Fprintf(out, "Free in %s  %s (a restore needs %s)\n", s.DataDir, humanize.Bytes(free), humanize.Bytes(uint64(m.Need())))
			}
			if len(args) == 0 {
				fmt.Fprintln(out, "\n`quai-node snapshot restore` replaces this node's chain data with it.")
				return nil
			}
			if underSudo() {
				return errors.New("run quai-node as your own user; it uses sudo itself where needed")
			}
			if !yes {
				ok := false
				err := huh.NewConfirm().
					Title(fmt.Sprintf("Replace the chain data in %s with this snapshot?", s.DataDir)).
					Description("The node stops while it downloads. quikshot.dev is an independent community service; you restore at your own risk.").
					Value(&ok).Run()
				if err != nil || !ok {
					return errors.New("nothing changed")
				}
			}
			mgr, _ := services.Detect(services.NewRunner(out, false))
			s.RestoreSnapshot = true
			if err := restoreSnapshot(out, h, &s, mgr); err != nil {
				return err
			}
			if mgr != nil && s.Service {
				if installed, ours, _ := services.Owns(mgr, services.Name, h); installed && ours {
					return mgr.Start(services.Name)
				}
			}
			fmt.Fprintln(out, "Start the node with `quai-node run`.")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}
