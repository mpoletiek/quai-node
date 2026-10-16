package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/mpoletiek/quai-node/internal/keepers"
	"github.com/mpoletiek/quai-node/internal/settings"
)

func keepersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keepers status|claim",
		Short: "This node on the Keepers at quainode.org: its checks, or claim it",
		Long: `quainode.org/nodes lists the Quai nodes that prove they keep the chain.
Setup turns reporting on (quai-node setup, or --keepers-tag).

'status' shows what the Keepers see: whether the node has logged in,
passed its checks, and been claimed.
'claim' signs your tag with the node's p2p key (config/private.key) and
sends the signature, which ties the tag to this node. The key never
leaves the machine. It works once the node has logged in.`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"status", "claim"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			h, s, err := loadHome()
			if err != nil {
				return err
			}
			if !s.Reporting() {
				return errors.New("this node doesn't report to the Keepers on quainode.org; turn it on with `quai-node setup`")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			switch args[0] {
			case "status":
				return keepersStatus(ctx, out, s, true)
			case "claim":
				text, err := keepers.Claim(ctx, filepath.Join(h.ConfigDir(), "private.key"), s.KeepersTag)
				if err != nil {
					return fmt.Errorf("claim: %w", err)
				}
				fmt.Fprintln(out, text)
				fmt.Fprintf(out, "%s/%s\n", keepers.Page, s.KeepersTag)
				return nil
			}
			return fmt.Errorf("unknown action %q", args[0])
		},
	}
	return cmd
}

// quainationCmd is `quai-node keepers` under its old name, from when the
// Keepers were on quaination.xyz: a hidden alias, so scripts and habits
// that call `quai-node quaination status|claim` keep working.
func quainationCmd() *cobra.Command {
	cmd := keepersCmd()
	cmd.Use = "quaination status|claim"
	cmd.Short = "Old name of `quai-node keepers`"
	cmd.Hidden = true
	return cmd
}

// keepersStatus prints what the Keepers say about the node: one line, or
// with full the details and what to do about them.
func keepersStatus(ctx context.Context, out io.Writer, s settings.Settings, full bool) error {
	st, err := keepers.Lookup(ctx, s.KeepersTag, s.KeepersSecret)
	if errors.Is(err, keepers.ErrUnknown) {
		fmt.Fprintf(out, "Keepers     %s: not logged in to keepers.quainode.org yet\n", s.KeepersTag)
		if full {
			fmt.Fprintln(out, "            go-quai logs in once it runs; if it doesn't show up, check that TCP 9200 is open to the internet.")
			fmt.Fprintf(out, "            %s\n", keepers.StatusPage)
		}
		return nil
	}
	if err != nil {
		fmt.Fprintf(out, "Keepers     %s: couldn't ask keepers.quainode.org (%v)\n", s.KeepersTag, err)
		return nil
	}
	line := fmt.Sprintf("Keepers     %s: %s", s.KeepersTag, st.Status)
	if st.StatusReason != "" {
		line += " (" + st.StatusReason + ")"
	}
	line += ", claim: " + st.Claim
	fmt.Fprintln(out, line)
	if !full {
		return nil
	}
	if st.HeadNumber > 0 {
		fmt.Fprintf(out, "            head %s, %s behind\n", humanize.Comma(int64(st.HeadNumber)), humanize.Comma(int64(st.Lag)))
	}
	if st.Node != "" {
		fmt.Fprintf(out, "            seen as node %s\n", st.Node)
	}
	if len(st.Checks) > 0 {
		c := st.Checks[0]
		check := "last check: " + c.Outcome
		if c.Step() != "" {
			check += " at " + c.Step()
		}
		if c.Reason != "" {
			check += " (" + c.Reason + ")"
		}
		if t, err := time.Parse(time.RFC3339, c.TS); err == nil {
			check += ", " + humanize.Time(t)
		}
		fmt.Fprintln(out, "            "+check)
	}
	switch st.Claim {
	case "none":
		fmt.Fprintln(out, "            claim it: quai-node keepers claim")
	case "mismatch":
		fmt.Fprintln(out, "            the tag is claimed by another node; if this node replaced it: quai-node keepers claim")
	}
	fmt.Fprintf(out, "            %s/%s\n", keepers.Page, s.KeepersTag)
	return nil
}

// lanURLs are the dashboard's addresses on this machine's networks, for a
// dashboard that listens on every interface.
func lanURLs(port string) []string {
	var urls []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		urls = append(urls, "http://"+net.JoinHostPort(ipn.IP.String(), port))
	}
	return urls
}
