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

	"github.com/mpoletiek/quai-node/internal/quaination"
	"github.com/mpoletiek/quai-node/internal/settings"
)

func quainationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quaination status|claim",
		Short: "This node on quaination.xyz's Keepers: its checks, or claim it",
		Long: `quaination.xyz/nodes lists the Quai nodes that prove they keep the chain.
Setup turns reporting on (quai-node setup, or --quaination-tag).

'status' shows what quaination.xyz sees: whether the node has logged in,
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
				return errors.New("this node doesn't report to quaination.xyz; turn it on with `quai-node setup`")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			switch args[0] {
			case "status":
				return quainationStatus(ctx, out, s, true)
			case "claim":
				text, err := quaination.Claim(ctx, filepath.Join(h.ConfigDir(), "private.key"), s.QuainationTag)
				if err != nil {
					return fmt.Errorf("claim: %w", err)
				}
				fmt.Fprintln(out, text)
				return nil
			}
			return fmt.Errorf("unknown action %q", args[0])
		},
	}
	return cmd
}

// quainationStatus prints what quaination.xyz says about the node: one
// line, or with full the details and what to do about them.
func quainationStatus(ctx context.Context, out io.Writer, s settings.Settings, full bool) error {
	st, err := quaination.Lookup(ctx, s.QuainationTag, s.QuainationSecret)
	if errors.Is(err, quaination.ErrUnknown) {
		fmt.Fprintf(out, "Keepers     %s: not logged in to quaination.xyz yet\n", s.QuainationTag)
		if full {
			fmt.Fprintln(out, "            go-quai logs in once it runs; if it doesn't show up, check that TCP 9200 is open to the internet.")
		}
		return nil
	}
	if err != nil {
		fmt.Fprintf(out, "Keepers     %s: couldn't ask quaination.xyz (%v)\n", s.QuainationTag, err)
		return nil
	}
	line := fmt.Sprintf("Keepers     %s: %s", s.QuainationTag, st.Status)
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
	if st.Claim == "none" {
		fmt.Fprintln(out, "            claim it: quai-node quaination claim")
	}
	fmt.Fprintf(out, "            %s/%s\n", quaination.Page, s.QuainationTag)
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
