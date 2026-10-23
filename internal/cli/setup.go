package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/mpoletiek/quai-node/internal/bundle"
	"github.com/mpoletiek/quai-node/internal/geo"
	"github.com/mpoletiek/quai-node/internal/home"
	"github.com/mpoletiek/quai-node/internal/keepers"
	"github.com/mpoletiek/quai-node/internal/onpath"
	"github.com/mpoletiek/quai-node/internal/quaiaddr"
	"github.com/mpoletiek/quai-node/internal/services"
	"github.com/mpoletiek/quai-node/internal/settings"
	"github.com/mpoletiek/quai-node/internal/snapshot"
	"github.com/mpoletiek/quai-node/internal/status"
	"github.com/mpoletiek/quai-node/internal/wizard"
)

type setupOpts struct {
	yes         bool // no questions: take flags and defaults
	apply       bool // no questions: rewrite configs and services from quai-node.toml
	dryRun      bool
	initKind    string
	network     string
	zone        string
	dataDir     string
	mine        bool
	quaiAddr    string
	qiAddr      string
	dashboard   bool
	noDash      bool
	service     bool
	noService   bool
	runAs       string
	p2pPort     int
	indexUTXOs  bool
	snapshot    bool
	fromScratch bool
	ignoreSpace bool
	dashPublic  bool
	keepersTag  string
	noKeepers   bool
	geoip       string
	geoipDB     string
	location    string
}

func setupCmd() *cobra.Command {
	var o setupOpts
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up (or change) the node: asks a few questions, then starts it",
		Long: `Set up the node: unpack go-quai (and quai-dash) into the home directory,
write their configs, and install and start the services.

Interactive by default. With --yes it asks nothing and takes the flags
and the defaults, for scripted installs; with --apply it re-applies
quai-node.toml after you edit it.`,
		Example: `  quai-node setup
  quai-node setup --yes --network orchard --no-service
  quai-node setup --yes --mine --quai-address 0x00… --qi-address 0x0080…
  quai-node setup --dry-run --yes        # show what would be installed`,
		RunE: func(cmd *cobra.Command, args []string) error { return runSetup(cmd, o) },
	}
	f := cmd.Flags()
	f.BoolVarP(&o.yes, "yes", "y", false, "ask nothing: use the flags and defaults")
	f.BoolVar(&o.apply, "apply", false, "ask nothing: re-apply the existing quai-node.toml")
	f.BoolVar(&o.dryRun, "dry-run", false, "show the files and commands, change nothing")
	f.StringVar(&o.initKind, "init", "", "service manager (Linux: systemd or openrc; default: detected)")
	f.StringVar(&o.network, "network", "", "colosseum (mainnet) or orchard (testnet)")
	f.StringVar(&o.zone, "zone", "", "zone to run, e.g. cyprus1 (default)")
	f.StringVar(&o.dataDir, "data-dir", "", "chain data directory")
	f.BoolVar(&o.mine, "mine", false, "run a stratum server for local miners")
	f.StringVar(&o.quaiAddr, "quai-address", "", "Quai payout address (with --mine)")
	f.StringVar(&o.qiAddr, "qi-address", "", "Qi payout address (with --mine)")
	f.BoolVar(&o.dashboard, "dashboard", true, "install the quai-dash web dashboard")
	f.BoolVar(&o.noDash, "no-dashboard", false, "don't install the dashboard")
	f.BoolVar(&o.service, "service", true, "run as a service (start at boot or login)")
	f.BoolVar(&o.noService, "no-service", false, "don't install services: run with `quai-node run`")
	f.StringVar(&o.runAs, "run-as", "", "Linux: user the services run as (default: you)")
	f.IntVar(&o.p2pPort, "p2p-port", 0, "P2P port, TCP and UDP (default 4002)")
	f.BoolVar(&o.indexUTXOs, "index-utxos", false, "run an index node: index every address's Qi UTXOs, for Qi balances and UTXO lists on the RPC (--index-utxos=false to stop)")
	f.BoolVar(&o.dashPublic, "dashboard-public", false, "let other machines open the dashboard (every interface; it has no login)")
	f.StringVar(&o.keepersTag, "keepers-tag", "", "report to the Keepers on quainode.org under this tag (makes the node's RPC public: open TCP 9200)")
	f.BoolVar(&o.noKeepers, "no-keepers", false, "stop reporting to the Keepers")
	// The flags' names from when the Keepers were on quaination.xyz: hidden,
	// deprecated aliases that set the same thing.
	f.StringVar(&o.keepersTag, "quaination-tag", "", "old name of --keepers-tag")
	f.BoolVar(&o.noKeepers, "no-quaination", false, "old name of --no-keepers")
	_ = f.MarkDeprecated("quaination-tag", "use --keepers-tag")
	_ = f.MarkDeprecated("no-quaination", "use --no-keepers")
	f.BoolVar(&o.snapshot, "snapshot", false, "start the chain from quikshot.dev's latest snapshot (mainnet cyprus1; switches go-quai to Pebble)")
	f.BoolVar(&o.fromScratch, "from-scratch", false, "delete the chain in the data directory, so the node syncs it again from the network")
	f.BoolVar(&o.ignoreSpace, "ignore-space", false, "set up even if the data directory looks too small for the chain")
	f.StringVar(&o.geoip, "geoip", "", "dashboard map: where peer locations come from: online (ip-api.com receives peer IPs), db, off or auto")
	f.StringVar(&o.geoipDB, "geoip-db", "", "dashboard map: a MaxMind City database (.mmdb), for --geoip db")
	f.StringVar(&o.location, "location", "", "dashboard map: this node as LAT,LON, auto (look it up from the public IP) or off")
	return cmd
}

func runSetup(cmd *cobra.Command, o setupOpts) error {
	out := cmd.OutOrStdout()
	if underSudo() {
		return errors.New("run quai-node as your own user, not through sudo: it sets the node up in your home and asks for sudo itself when installing services")
	}
	h, err := theHome()
	if err != nil {
		return err
	}
	m, err := bundle.Read()
	if err != nil {
		return err
	}

	s := settings.Defaults(h)
	if h.Exists() {
		if s, err = settings.Load(h); err != nil {
			return err
		}
	}
	if m.DashVersion == "" {
		s.Dashboard = false
	}
	if s.RunAs == "" {
		s.RunAs = currentUser()
	}

	runner := services.NewRunner(out, o.dryRun)
	mgr, mgrErr := manager(runner, o.initKind)

	held := 0 // the P2P port this install's node may be listening on now
	var height uint64
	if h.Exists() {
		held = s.P2PPort
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if n := status.Read(ctx, s.ZoneRPC(), ""); n.Reachable {
			height = n.Height
		}
		cancel()
	}
	heldData := s.DataDir
	router := routerMappings()        // who else uses which port, meanwhile
	snap, snapErr := latestSnapshot() // the chain's size today, and a snapshot to offer
	mappings := <-router
	interactive := !o.yes && !o.apply && isatty.IsTerminal(os.Stdin.Fd())
	if interactive {
		// Flags given with the wizard are its starting answers.
		if err := applyFlags(cmd, &s, o, m); err != nil {
			return err
		}
		pc := checkPort(s.P2PPort, held, mappings)
		info := wizard.Info{Home: h, GoQuaiVersion: m.GoQuaiVersion, DashVersion: m.DashVersion,
			Snapshot: snap, SnapshotErr: snapErr, SpaceShort: func(s settings.Settings) string { return spaceShort(s, snap) },
			PortConflict: pc.Conflict, PortSuggest: pc.Suggest, ChainHeight: height, ChainDir: heldData}
		f := cmd.Flags()
		info.ServiceSet = f.Changed("service") || o.noService
		info.DashboardSet = f.Changed("dashboard") || o.noDash
		info.IndexSet = f.Changed("index-utxos")
		info.IgnoreSpace = o.ignoreSpace
		if mgrErr == nil {
			info.ServiceKind = mgr.Kind()
		} else {
			info.ServiceNote = mgrErr.Error()
		}
		if err := wizard.Run(&s, info); err != nil {
			return err
		}
	} else if !o.apply {
		if err := applyFlags(cmd, &s, o, m); err != nil {
			return err
		}
		pc := checkPort(s.P2PPort, held, mappings)
		if pc.Conflict != nil && o.p2pPort == 0 && pc.Suggest != 0 {
			fmt.Fprintf(out, "%s\nUsing port %d instead (--p2p-port to choose).\n", pc.Conflict.Problem(), pc.Suggest)
			s.P2PPort = pc.Suggest
		} else {
			warnPort(out, pc)
		}
	} else {
		warnPort(out, checkPort(s.P2PPort, held, mappings))
	}
	// A chain that stays must be opened with the engine it was made with.
	if e := home.ChainEngine(s.DataDir); e != "" && !s.RestoreSnapshot && !s.WipeChain && e != s.Engine() {
		fmt.Fprintf(out, "The chain in %s is a %s database: the node will use %s.\n", s.DataDir, e, e)
		s.DBEngine = e
	}
	if s.Service && mgrErr != nil {
		return fmt.Errorf("can't install services: %w (or pass --no-service)", mgrErr)
	}
	if err := s.Validate(); err != nil {
		return err
	}
	if err := checkIndex(s); err != nil {
		return err
	}
	// Turned away now rather than halfway through: Save merges it.
	extra, err := settings.LoadExtra(h)
	if err != nil {
		return err
	}
	after := s // the engine a snapshot switches to
	if s.RestoreSnapshot {
		after.DBEngine = "pebble"
	}
	if err := settings.CheckExtra(h, extra, after); err != nil {
		return err
	}
	if !interactive && !o.ignoreSpace {
		if short := spaceShort(s, snap); short != "" {
			return errors.New(short + " (--ignore-space to set up anyway)")
		}
	}

	// Before writing anything: the machine's quai-node service, if any,
	// must be this home's for setup to replace it.
	if mgr != nil && s.Service {
		if _, err := ownService(mgr, services.Name, h); err != nil {
			return err
		}
	}

	var pathNote string
	fmt.Fprintf(out, "\nSetting up in %s\n", h.Dir)
	if o.dryRun {
		fmt.Fprintln(out, "(dry run: nothing is changed)")
		fmt.Fprintln(out, wizard.Summary(s, wizard.Info{Home: h, ServiceKind: kindOf(mgr)}))
	} else {
		if !bundle.Current(h) {
			fmt.Fprintf(out, "  unpack go-quai %s", m.GoQuaiVersion)
			if s.Dashboard {
				fmt.Fprintf(out, " and quai-dash %s", m.DashVersion)
			}
			fmt.Fprintf(out, " into %s\n", h.BinDir())
			if _, err := bundle.Install(h); err != nil {
				return err
			}
		}
		if err := bundle.CopySelf(h); err != nil {
			return fmt.Errorf("copy quai-node into %s: %w", h.BinDir(), err)
		}
		pathNote = putOnPath(out, h, s.Dashboard)
		fmt.Fprintf(out, "  write %s\n  write %s\n", h.Settings(), h.NodeConfig())
		if names := settings.ExtraNames(extra); len(names) > 0 {
			fmt.Fprintf(out, "    with your keys from %s: %s\n", h.ExtraConfig(), strings.Join(names, ", "))
		}
		if s.Dashboard {
			fmt.Fprintf(out, "  write %s\n", h.DashConfig())
		}
		if err := settings.Save(h, s); err != nil {
			return err
		}
	}
	if !s.RestoreSnapshot { // a restore clears it itself
		if n := snapshot.Leftover(s.DataDir); n > 0 {
			fmt.Fprintf(out, "  remove an unfinished snapshot download in %s (%s)\n", s.DataDir, humanize.Bytes(uint64(n)))
			if !o.dryRun {
				if err := snapshot.RemoveLeftover(s.DataDir); err != nil {
					return err
				}
			}
		}
	}

	if s.WipeChain {
		if o.dryRun {
			fmt.Fprintf(out, "\nDelete the chain in %s (%s), so the node syncs it from scratch\n", s.DataDir, humanize.Bytes(uint64(home.ChainUse(s.DataDir))))
		} else if err := wipeChain(out, h, s, mgr); err != nil {
			return err
		}
	}
	if s.RestoreSnapshot {
		if o.dryRun {
			fmt.Fprintf(out, "\nRestore quikshot.dev's latest snapshot into %s, then switch go-quai to the Pebble engine\n", s.DataDir)
		} else if err := restoreSnapshot(out, h, &s, mgr); err != nil {
			return err
		}
	}
	if !o.dryRun {
		if err := markIndex(s); err != nil {
			return err
		}
	}

	if mgr != nil {
		// Earlier builds installed go-quai and quai-dash as two services.
		for _, name := range services.Legacy {
			if installed, ours, _ := services.Owns(mgr, name, h); installed && ours {
				fmt.Fprintf(out, "\nRemoving the separate %s service (now part of the quai-node service)\n", name)
				if err := mgr.Uninstall(name); err != nil {
					return err
				}
			}
		}
		if s.Service {
			fmt.Fprintf(out, "\nInstalling the %s service %s\n", mgr.Kind(), services.Name)
			if err := mgr.Install(serviceFor(h, s)); err != nil {
				return fmt.Errorf("install the %s service: %w", services.Name, err)
			}
		} else if installed, ours, _ := services.Owns(mgr, services.Name, h); installed && ours {
			fmt.Fprintf(out, "\nRemoving the %s service\n", services.Name)
			if err := mgr.Uninstall(services.Name); err != nil {
				return err
			}
		}
	}
	if o.dryRun {
		return nil
	}

	fmt.Fprintln(out, "\nDone.")
	if s.Service {
		fmt.Fprintln(out, "The node is starting. It syncs from the network first, which takes a while.")
		fmt.Fprintln(out, "Start and stop it (and the dashboard) with:  quai-node service start|stop")
	} else {
		fmt.Fprintln(out, "Start the node with:  quai-node run")
	}
	fmt.Fprintln(out, "Its progress:         quai-node status")
	if s.Dashboard {
		if s.DashboardPublic() {
			fmt.Fprintf(out, "Dashboard:            http://127.0.0.1:%s here", s.DashboardPort())
			for _, u := range lanURLs(s.DashboardPort()) {
				fmt.Fprintf(out, ", %s", u)
			}
			fmt.Fprintln(out, "\n                      (no login: keep it to a trusted network; a firewall may need port "+s.DashboardPort()+" opened)")
		} else {
			fmt.Fprintf(out, "Dashboard:            http://%s\n", s.DashboardListen)
		}
		if homeFlag == "" {
			fmt.Fprintln(out, "In a terminal:        quai-dash tui")
		} else {
			fmt.Fprintf(out, "In a terminal:        %s --config %s tui\n", h.QuaiDash(), h.DashConfig())
		}
	}
	if s.Reporting() {
		fmt.Fprintf(out, "\nKeepers:              reporting to keepers.quainode.org as %s. To be verified there:\n", s.KeepersTag)
		fmt.Fprintln(out, "  • open TCP 9200 to the internet (forward it on your router, allow it in any firewall);")
		fmt.Fprintf(out, "  • make sure P2P port %d is reachable too.\n", s.P2PPort)
		fmt.Fprintln(out, "  Then: quai-node keepers status    (once it has logged in, the node claims its tag by itself)")
		fmt.Fprintf(out, "  Your node's page: %s/%s\n", keepers.Page, s.KeepersTag)
		fmt.Fprintf(out, "  Your tag's secret is in %s; keep it if you move the node.\n", h.Settings())
	}
	if s.Mining {
		fmt.Fprintln(out, "Point your miners at this machine: ports 3333 (KawPoW), 3334 (Scrypt), 3335 (SHA-256).")
	}
	fmt.Fprintln(out, "\nLogs")
	fmt.Fprintln(out, services.LogNote(serviceKind(s, mgr), h))
	if pathNote != "" {
		fmt.Fprintln(out, "\n"+pathNote)
	}
	return nil
}

// putOnPath makes `quai-node` (and, with the dashboard, `quai-dash`) a
// command, and points quai-dash's default config at this node, so a plain
// `quai-dash tui` watches it. Only for the default home: a --home install
// (a test, a second node) leaves them alone. It returns what the user still
// has to do, if anything.
func putOnPath(out io.Writer, h home.Home, dashboard bool) string {
	if homeFlag != "" {
		return ""
	}
	bins := []string{h.QuaiNode()}
	if dashboard {
		bins = append(bins, h.QuaiDash())
	}
	var note string
	for _, b := range bins {
		r, err := onpath.Add(b)
		if err != nil {
			return filepath.Base(b) + " isn't on your PATH: " + err.Error()
		}
		fmt.Fprintf(out, "  put %s on your PATH: %s\n", filepath.Base(b), r.Path)
		note = r.Note
	}
	if dashboard {
		if p, ok, err := onpath.LinkDashConfig(h.DashConfig()); err != nil {
			fmt.Fprintf(out, "  (quai-dash's default config: %v)\n", err)
		} else if ok {
			fmt.Fprintf(out, "  link %s -> %s\n", p, h.DashConfig())
		} else if p != "" {
			fmt.Fprintf(out, "  keep your own %s; `quai-dash --config %s tui` watches this node\n", p, h.DashConfig())
		}
	} else {
		_ = onpath.Remove(h.QuaiDash())
		_ = onpath.UnlinkDashConfig(h.DashConfig())
	}
	return note
}

// serviceKind is the manager the node runs under, or "" in the foreground.
func serviceKind(s settings.Settings, mgr services.Manager) string {
	if !s.Service || mgr == nil {
		return ""
	}
	return mgr.Kind()
}

func manager(r *services.Runner, kind string) (services.Manager, error) {
	if kind != "" {
		return services.ForceKind(r, kind)
	}
	return services.Detect(r)
}

func kindOf(m services.Manager) string {
	if m == nil {
		return ""
	}
	return m.Kind()
}

// applyFlags sets s from the flags given, for setup --yes.
func applyFlags(cmd *cobra.Command, s *settings.Settings, o setupOpts, m bundle.Manifest) error {
	f := cmd.Flags()
	if o.network != "" {
		s.Network = o.network
	}
	if o.zone != "" {
		z, err := parseZone(o.zone)
		if err != nil {
			return err
		}
		s.Region, s.Zone = z.Region, z.Zone
	}
	if o.dataDir != "" {
		abs, err := filepath.Abs(o.dataDir)
		if err != nil {
			return err
		}
		s.DataDir = abs
	}
	if f.Changed("mine") {
		s.Mining = o.mine
	}
	if o.quaiAddr != "" {
		s.QuaiCoinbase = o.quaiAddr
	}
	if o.qiAddr != "" {
		s.QiCoinbase = o.qiAddr
	}
	if f.Changed("dashboard") {
		s.Dashboard = o.dashboard
		if o.dashboard && m.DashVersion == "" {
			return errors.New("--dashboard: this quai-node carries no quai-dash for this platform")
		}
	}
	if o.noDash {
		s.Dashboard = false
	}
	if f.Changed("service") {
		s.Service = o.service
	}
	if o.noService {
		s.Service = false
	}
	if o.runAs != "" {
		s.RunAs = o.runAs
	}
	if o.p2pPort != 0 {
		s.P2PPort = o.p2pPort
	}
	if f.Changed("index-utxos") {
		s.IndexUTXOs = o.indexUTXOs
	}
	if f.Changed("dashboard-public") {
		host := "127.0.0.1"
		if o.dashPublic {
			host = "0.0.0.0"
		}
		s.DashboardListen = net.JoinHostPort(host, s.DashboardPort())
	}
	if o.keepersTag != "" {
		if o.keepersTag != s.KeepersTag || s.KeepersSecret == "" {
			s.KeepersSecret = keepers.NewSecret()
		}
		s.KeepersTag = o.keepersTag
	}
	if o.noKeepers {
		s.KeepersTag, s.KeepersSecret = "", ""
	}
	if o.snapshot && o.fromScratch {
		return errors.New("--snapshot or --from-scratch, not both")
	}
	s.WipeChain = o.fromScratch
	if o.snapshot {
		if !snapshotFits(*s) {
			return errors.New("--snapshot: quikshot.dev's snapshots are for mainnet (colosseum) nodes running cyprus1")
		}
		s.RestoreSnapshot = true
	}
	if o.geoip != "" {
		s.GeoIP = o.geoip
		if o.geoip != geo.DB {
			s.GeoIPDB = ""
		}
	}
	if o.geoipDB != "" {
		abs, err := filepath.Abs(o.geoipDB)
		if err != nil {
			return err
		}
		s.GeoIPDB = abs
	} else if s.GeoIP == geo.DB && s.GeoIPDB == "" {
		s.GeoIPDB = geo.FindDB()
	}
	switch o.location {
	case "":
	case "off":
		s.Location = ""
	case "auto":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		p, err := geo.Detect(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("--location auto: %w", err)
		}
		s.Location = p.LatLon()
		fmt.Fprintf(cmd.OutOrStdout(), "Located this node at %s (%s, via %s)\n", s.Location, p.City, p.Source)
	default:
		p, err := geo.ParseLatLon(o.location)
		if err != nil {
			return fmt.Errorf("--location: %w", err)
		}
		s.Location = p.LatLon()
	}
	return nil
}

func parseZone(name string) (quaiaddr.Zone, error) {
	var names []string
	for _, z := range quaiaddr.Zones() {
		if strings.EqualFold(z.Name(), name) {
			return z, nil
		}
		names = append(names, z.Name())
	}
	return quaiaddr.Zone{}, fmt.Errorf("--zone %q: expected one of %s", name, strings.Join(names, ", "))
}

// ensureBundle unpacks the payload into h when h holds an older one.
func ensureBundle(h home.Home) error {
	if bundle.Current(h) {
		return nil
	}
	if _, err := bundle.Install(h); err != nil {
		return err
	}
	return bundle.CopySelf(h)
}
