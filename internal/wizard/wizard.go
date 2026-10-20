// Package wizard asks the questions setup needs, in the terminal.
package wizard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/dustin/go-humanize"
	"golang.org/x/term"

	"github.com/mpoletiek/quai-node/internal/geo"
	"github.com/mpoletiek/quai-node/internal/home"
	"github.com/mpoletiek/quai-node/internal/keepers"
	"github.com/mpoletiek/quai-node/internal/quaiaddr"
	"github.com/mpoletiek/quai-node/internal/services"
	"github.com/mpoletiek/quai-node/internal/settings"
	"github.com/mpoletiek/quai-node/internal/snapshot"
	"github.com/mpoletiek/quai-node/internal/upnp"
)

// Info is what the wizard tells the user about this build and host.
type Info struct {
	Home          home.Home
	GoQuaiVersion string
	DashVersion   string // empty: no dashboard for this platform
	ServiceKind   string // systemd, openrc, launchd, windows; empty: none
	ServiceNote   string // why there is none, or what installing needs
	// ServiceSet and DashboardSet: a flag settled it, so it isn't asked.
	ServiceSet   bool
	DashboardSet bool
	// IndexSet: --index-utxos settled whether this is an index node.
	IndexSet bool
	// Snapshot is quikshot.dev's latest snapshot, nil when unknown; its
	// unpacked size is also the best estimate of the chain's size today.
	Snapshot *snapshot.Manifest
	// SnapshotErr is why Snapshot is nil.
	SnapshotErr error
	// PortConflict is another machine or program already using the P2P
	// port, nil if none; PortSuggest a free port instead (0: none found).
	PortConflict *upnp.Conflict
	PortSuggest  int
	// ChainHeight is the zone height of this install's running node, whose
	// chain is in ChainDir; 0 when it isn't answering.
	ChainHeight uint64
	ChainDir    string
	// SpaceShort says how much space the settings' data directory lacks,
	// "" when enough (or unknown).
	SpaceShort func(settings.Settings) string
	// IgnoreSpace: --ignore-space was given, so a short disk isn't asked about.
	IgnoreSpace bool
}

// ErrAborted means the user backed out.
var ErrAborted = errors.New("setup cancelled")

// Run asks, starting from s, and fills s in. It returns ErrAborted when the
// user cancels or declines the summary.
func Run(s *settings.Settings, info Info) error {
	mode := "node"
	if s.Mining {
		mode = "mine"
	}
	welcome := fmt.Sprintf("This sets up a Quai Network node (go-quai %s) in\n%s\n\nIt asks a few questions, then starts the node. Run 'quai-node setup' again to change anything.",
		info.GoQuaiVersion, info.Home.Dir)

	zone := s.ZoneOf()
	// Separate forms rather than hidden groups: the plain, line-by-line
	// mode doesn't honor a group's hide function.
	if err := ask(huh.NewGroup(
		huh.NewNote().Title("quai-node setup").Description(welcome),
		huh.NewSelect[string]().
			Title("Network").
			Options(
				huh.NewOption("Colosseum: Quai mainnet", "colosseum"),
				huh.NewOption("Orchard: testnet", "orchard"),
			).
			Value(&s.Network),
		huh.NewSelect[string]().
			Title("What is this node for?").
			Options(
				huh.NewOption("Run a node: sync and help the network", "node"),
				huh.NewOption("Mine: also run a stratum server for my miners, paying my addresses", "mine"),
			).
			Value(&mode),
	)); err != nil {
		return err
	}
	if mode == "mine" {
		if err := ask(huh.NewGroup(
			huh.NewNote().Title("Payout addresses").Description(fmt.Sprintf(
				"go-quai pays block rewards to one Quai and one Qi address, both in %s, the zone this node runs.\nMiners connect to this machine on ports 3333 (KawPoW), 3334 (Scrypt) and 3335 (SHA-256).", zone.Name())),
			huh.NewInput().Title("Quai address").Description("0x00… (a Quai address in this zone)").
				Value(&s.QuaiCoinbase).
				Validate(func(a string) error { return quaiaddr.Check(a, quaiaddr.Quai, zone) }),
			huh.NewInput().Title("Qi address").Description("0x0080… (a Qi address in this zone)").
				Value(&s.QiCoinbase).
				Validate(func(a string) error { return quaiaddr.Check(a, quaiaddr.Qi, zone) }),
		)); err != nil {
			return err
		}
	}

	defaultData := s.DataDir
	dataTitle := "Where should the chain data go?"
	if plain() { // line-by-line input starts empty: say what Enter keeps
		dataTitle += " (Enter for " + defaultData + ")"
	}
	fields := []huh.Field{
		huh.NewInput().
			Title(dataTitle).
			DescriptionFunc(func() string { return freeNote(s.DataDir) }, &s.DataDir).
			Value(&s.DataDir).
			Validate(func(p string) error {
				if p = strings.TrimSpace(p); p != "" && !filepath.IsAbs(p) {
					return errors.New("give an absolute path")
				}
				return nil
			}),
	}
	if info.DashVersion != "" && !info.DashboardSet {
		fields = append(fields, huh.NewConfirm().
			Title("Install the web dashboard (quai-dash)?").
			Description(fmt.Sprintf("A live view of the node: chain heights, peers, mining, logs. It listens on http://%s, on this machine only.", s.DashboardListen)).
			Value(&s.Dashboard))
	} else if info.DashVersion == "" {
		s.Dashboard = false
	}
	switch {
	case info.ServiceKind == "":
		s.Service = false
		fields = append(fields, huh.NewNote().Title("Running in the foreground").
			Description(info.ServiceNote+"\nStart the node with 'quai-node run'."))
	case !info.ServiceSet: // else --service or --no-service answered it
		fields = append(fields, huh.NewConfirm().
			Title("Keep the node running in the background?").
			Description(serviceDescription(info)).
			Value(&s.Service))
	}
	if err := ask(huh.NewGroup(fields...)); err != nil {
		return err
	}
	if err := askPort(s, info); err != nil {
		return err
	}
	if err := askIndex(s, info); err != nil {
		return err
	}
	if err := askChain(s, info); err != nil {
		return err
	}
	if s.Dashboard {
		if err := askMap(s); err != nil {
			return err
		}
		if err := askAccess(s); err != nil {
			return err
		}
	}
	if s.Network == "colosseum" && s.ZoneOf() == (quaiaddr.Zone{}) {
		if err := askKeepers(s); err != nil {
			return err
		}
	} else {
		s.KeepersTag, s.KeepersSecret = "", ""
	}
	s.Mining = mode == "mine"
	if strings.TrimSpace(s.DataDir) == "" {
		s.DataDir = defaultData
	}
	s.DataDir = filepath.Clean(strings.TrimSpace(s.DataDir))
	if !s.Mining {
		s.QuaiCoinbase, s.QiCoinbase = "", ""
	}

	ok := true
	if err := ask(huh.NewGroup(
		huh.NewNote().Title("Ready").Description(Summary(*s, info)),
		huh.NewConfirm().Title("Set it up?").Affirmative("Yes").Negative("Back out").Value(&ok),
	)); err != nil {
		return err
	}
	if !ok {
		return ErrAborted
	}
	return nil
}

// askIndex asks whether this is an index node. go-quai indexes only the
// blocks it processes while indexing is on, and quai-node doesn't reindex
// (go-quai's opens the database as LevelDB, and fails on Pebble chains), so
// an index node's chain is indexed from its first block: synced from the
// network, never a snapshot. askChain then offers only that.
func askIndex(s *settings.Settings, info Info) error {
	if info.IndexSet {
		return nil
	}
	start := "The index has to start at the chain's first block, so an index node syncs the whole chain from the network (slow: days or more); it can't start from quikshot.dev's snapshot."
	switch used := home.ChainUse(s.DataDir); {
	case used > 0 && home.IndexedFromStart(s.DataDir):
		start = fmt.Sprintf("The chain in %s is indexed from its first block. Turned off, the index stops for good: turning it on again later means syncing from scratch.", s.DataDir)
	case used > 0:
		start += fmt.Sprintf(" The chain already in %s wasn't indexed, so it would have to be deleted.", s.DataDir)
	}
	return ask(huh.NewGroup(
		huh.NewNote().Title("Index node").Description(noteText(
			"An index node keeps an index of every address's Qi UTXOs. Qi balances (quai_getBalance) and UTXO lists "+
				"(quai_getOutpointsByAddress) on this node's RPC come from it; without it they come back zero or empty. "+
				"Wallets, explorers and other services need that; a node that only syncs and mines doesn't, so most don't run one. "+
				"It takes extra disk space and work on every block.\n\n"+start)),
		huh.NewConfirm().Title("Run an index node?").
			Affirmative("Yes, index Qi UTXOs").Negative("No").
			Value(&s.IndexUTXOs)))
}

// askIndexChain is the chain step for an index node: its chain must be
// indexed from the first block, so it keeps such a chain or syncs one from
// the network.
func askIndexChain(s *settings.Settings, info Info) error {
	s.RestoreSnapshot, s.WipeChain = false, false
	used := home.ChainUse(s.DataDir)
	switch {
	case used > 0 && home.IndexedFromStart(s.DataDir):
		if err := keepOrWipe(s, chainHere(s.DataDir, used, info), used); err != nil {
			return err
		}
	case used > 0:
		for {
			wipe := true
			if err := ask(huh.NewGroup(
				huh.NewNote().Title("Chain data").Description(fmt.Sprintf(
					"%s holds a chain (%s) that wasn't indexed from its first block, and an index node's must be.", s.DataDir, chainHere(s.DataDir, used, info))),
				huh.NewSelect[bool]().Title("What should happen?").Options(
					huh.NewOption("Delete it and sync from scratch as an index node (slow: days or more)", true),
					huh.NewOption("Keep it, and don't run an index node", false),
				).Value(&wipe))); err != nil {
				return err
			}
			if !wipe {
				s.IndexUTXOs = false
				return askChain(s, info)
			}
			sure, err := confirmWipe(s.DataDir, used)
			if err != nil {
				return err
			}
			if sure {
				s.WipeChain = true
				break
			}
		}
	default:
		if err := ask(huh.NewGroup(huh.NewNote().Title("Chain data").Description(
			"As an index node, the node syncs the chain from the network and indexes it from the first block (slow: days or more)."))); err != nil {
			return err
		}
	}
	return confirmSpace(s, info)
}

// noteText keeps a note's underscores (quai_getBalance): drawn notes take
// _x_ as italics, line-by-line ones print the text as it is.
func noteText(s string) string {
	if plain() {
		return s
	}
	return strings.ReplaceAll(s, "_", `\_`)
}

// ask runs one form, drawn or line by line.
func ask(g *huh.Group) error {
	err := huh.NewForm(g).WithAccessible(plain()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

// askMap asks how the dashboard's map places the peers and this node.
func askMap(s *settings.Settings) error {
	db := geo.FindDB()
	var opts []huh.Option[string]
	if db != "" {
		opts = append(opts, huh.NewOption("From the local database "+db+" (nothing leaves this machine)", geo.DB))
	}
	opts = append(opts,
		huh.NewOption("Look them up online: ip-api.com receives your peers' IP addresses", geo.Online),
		huh.NewOption("Don't place them", geo.Off),
	)
	peers := s.GeoIP
	if peers == geo.Auto || peers == "" || (peers == geo.DB && db == "") {
		peers = geo.Online
		if db != "" {
			peers = geo.DB
		}
	}
	where := "detect"
	if s.Location != "" {
		where = "manual"
	}
	if err := ask(huh.NewGroup(
		huh.NewNote().Title("The dashboard's map").Description(
			"The map shows where your peers are, and this node among them. Neither the node nor its RPC knows where a peer is, so quai-dash looks up their IP addresses."+
				map[bool]string{true: "", false: "\nWith a free MaxMind GeoLite2 City database in ~/.local/share/GeoIP or /usr/share/GeoIP, lookups stay on this machine."}[db != ""]),
		huh.NewSelect[string]().Title("Place peers on the map").Options(opts...).Value(&peers),
		huh.NewSelect[string]().Title("Place this node on the map").Options(
			huh.NewOption("Detect it from this machine's public IP (one lookup now, at ipinfo.io)", "detect"),
			huh.NewOption("Enter it myself", "manual"),
			huh.NewOption("Leave it off the map", "none"),
		).Value(&where),
	)); err != nil {
		return err
	}
	s.GeoIP, s.GeoIPDB = peers, ""
	if peers == geo.DB {
		s.GeoIPDB = db
	}

	loc, desc, found := s.Location, "LAT,LON, e.g. 47.4,8.5. It is rounded to a tenth of a degree, about 10 km.", ""
	switch where {
	case "none":
		s.Location = ""
		return nil
	case "detect":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		p, err := geo.Detect(ctx)
		cancel()
		if err != nil {
			desc = "Couldn't detect it (" + err.Error() + "). " + desc + " Leave it empty to keep the node off the map."
		} else {
			loc, found = p.LatLon(), ", "+orUnknown(p.City)
			desc = fmt.Sprintf("Found %s (%s) at %s. Behind a VPN that is the VPN's exit: correct it if so, or empty it to keep the node off the map.", orUnknown(p.City), p.Source, loc)
		}
	}
	title := "This node's location"
	if plain() && loc != "" { // plain mode shows no description: name the place here
		title += " (Enter for " + loc + found + ")"
	}
	def := loc
	if err := ask(huh.NewGroup(huh.NewInput().Title(title).Description(desc).Value(&loc).
		Validate(func(v string) error {
			if strings.TrimSpace(v) == "" {
				return nil
			}
			_, err := geo.ParseLatLon(v)
			return err
		}))); err != nil {
		return err
	}
	if strings.TrimSpace(loc) == "" && plain() {
		loc = def // line-by-line input starts empty: Enter keeps the suggestion
	}
	s.Location = ""
	if p, err := geo.ParseLatLon(loc); err == nil {
		s.Location = p.LatLon()
	}
	return nil
}

// askChain shows what the chain needs on disk and, for mainnet cyprus1,
// offers quikshot.dev's snapshot instead of a sync from the network.
func askChain(s *settings.Settings, info Info) error {
	if s.IndexUTXOs {
		return askIndexChain(s, info)
	}
	m := info.Snapshot
	fits := s.Network == "colosseum" && s.ZoneOf() == (quaiaddr.Zone{})
	used := home.ChainUse(s.DataDir)
	if m == nil && fits && info.SnapshotErr != nil && used == 0 {
		// Say why there's no snapshot to offer rather than skip the step.
		s.RestoreSnapshot = false
		return ask(huh.NewGroup(huh.NewNote().Title("Chain data").Description(
			info.SnapshotErr.Error() + ".\nThe node syncs the chain from the network (slow: days or more). " +
				"To start from a snapshot later, run 'quai-node snapshot restore'.")))
	}
	if m == nil || info.SpaceShort == nil {
		if used == 0 {
			return nil
		}
		// No snapshot to offer or measure by: keep the chain, or delete it.
		return keepOrWipe(s, chainHere(s.DataDir, used, info), used)
	}
	size := humanize.Bytes(uint64(m.ExpandedSizeBytes))
	free := "unknown"
	if f, err := home.FreeBytes(s.DataDir); err == nil {
		free = humanize.Bytes(f)
	}
	have := ""
	if used > 0 {
		have = chainHere(s.DataDir, used, info)
	}
	var note string
	switch {
	case have != "":
		note = fmt.Sprintf("%s already holds a chain: %s. Quai mainnet's chain is about %s today, and %s has %s free.", s.DataDir, have, size, s.DataDir, free)
	case fits:
		note = fmt.Sprintf("Quai mainnet's chain is about %s today and grows (the size of quikshot.dev's latest snapshot). %s has %s free.", size, s.DataDir, free)
	default:
		note = fmt.Sprintf("For scale: Quai mainnet's chain is about %s today. %s has %s free.", size, s.DataDir, free)
	}
	// Keep a chain that's here; otherwise the snapshot is the fast path,
	// offered first when it fits on disk.
	way := "sync"
	switch {
	case have != "":
		way = "keep"
		if s.RestoreSnapshot {
			way = "snapshot"
		}
	case fits:
		trial := *s
		trial.RestoreSnapshot = true
		if s.RestoreSnapshot || info.SpaceShort(trial) == "" {
			way = "snapshot"
		}
	}
	fields := []huh.Field{huh.NewNote().Title("Chain data").Description(note)}
	if fits {
		desc := "quikshot.dev is an independent community service, not affiliated with the Quai Network team. " +
			"quai-node checks the download against its published SHA-256 before using any of it, and switches the node to the Pebble database the snapshot is made of. "
		if !m.RestoreTested {
			desc += "quikshot.dev hasn't test-restored this snapshot; you restore at your own risk. "
		}
		snap := fmt.Sprintf("%s download, from %s ago", humanize.Bytes(uint64(m.SizeBytes)), m.Age())
		var opts []huh.Option[string]
		if have != "" {
			desc += "Replacing the chain here needs room for the whole snapshot beside it until the download checks out. "
			opts = []huh.Option[string]{
				huh.NewOption("Keep the chain that's here and sync on from it", "keep"),
				huh.NewOption(fmt.Sprintf("Replace it with quikshot.dev's snapshot (%s), then sync the rest", snap), "snapshot"),
				huh.NewOption("Delete it and sync from scratch (slow: days or more)", "scratch"),
			}
		} else {
			opts = []huh.Option[string]{
				huh.NewOption(fmt.Sprintf("Download quikshot.dev's snapshot (%s), then sync the rest", snap), "snapshot"),
				huh.NewOption("Sync it all from the network (slow: days or more)", "sync"),
			}
		}
		fields = append(fields, huh.NewSelect[string]().Title("How should the node get the chain?").Description(desc).Options(opts...).Value(&way))
	} else if have != "" {
		fields = append(fields, huh.NewSelect[string]().Title("What should happen to it?").Options(
			huh.NewOption("Keep it and sync on from it", "keep"),
			huh.NewOption("Delete it and sync from scratch (slow: days or more)", "scratch"),
		).Value(&way))
	}
	for {
		if err := ask(huh.NewGroup(fields...)); err != nil {
			return err
		}
		if way != "scratch" {
			break
		}
		sure, err := confirmWipe(s.DataDir, used)
		if err != nil {
			return err
		}
		if sure {
			break
		}
		way = "keep"
	}
	s.RestoreSnapshot = way == "snapshot"
	s.WipeChain = way == "scratch"
	return confirmSpace(s, info)
}

// confirmSpace asks whether to go on when the data directory looks too
// small for the chain.
func confirmSpace(s *settings.Settings, info Info) error {
	if info.SpaceShort == nil || info.IgnoreSpace {
		return nil
	}
	short := info.SpaceShort(*s)
	if short == "" {
		return nil
	}
	goOn := false
	if err := ask(huh.NewGroup(huh.NewConfirm().
		Title("Not enough disk space: " + short + ". Continue anyway?").
		Description("Or stop, and run setup again with another data directory, or free some space.").
		Affirmative("Continue anyway").Negative("Stop").Value(&goOn))); err != nil {
		return err
	}
	if !goOn {
		return ErrAborted
	}
	return nil
}

// keepOrWipe asks what happens to the chain in s.DataDir when there's no
// snapshot to offer instead.
func keepOrWipe(s *settings.Settings, have string, used int64) error {
	way := "keep"
	for {
		if err := ask(huh.NewGroup(
			huh.NewNote().Title("Chain data").Description(fmt.Sprintf("%s already holds a chain: %s.", s.DataDir, have)),
			huh.NewSelect[string]().Title("What should happen to it?").Options(
				huh.NewOption("Keep it and sync on from it", "keep"),
				huh.NewOption("Delete it and sync from scratch (slow: days or more)", "scratch"),
			).Value(&way))); err != nil {
			return err
		}
		if way != "scratch" {
			break
		}
		sure, err := confirmWipe(s.DataDir, used)
		if err != nil {
			return err
		}
		if sure {
			break
		}
		way = "keep"
	}
	s.RestoreSnapshot, s.WipeChain = false, way == "scratch"
	return nil
}

// confirmWipe asks again before the chain in dir is deleted.
func confirmWipe(dir string, used int64) (bool, error) {
	sure := false
	err := ask(huh.NewGroup(huh.NewConfirm().
		Title(fmt.Sprintf("Delete the chain in %s (%s)?", dir, humanize.Bytes(uint64(used)))).
		Description("It can't be undone: the node syncs the whole chain again from the network.").
		Affirmative("Delete it").Negative("Go back").Value(&sure)))
	return sure, err
}

// chainHere describes the chain in dir: "zone block 10,575,331, 303 GB,
// Pebble"; the block when this install's node answers for that dir.
func chainHere(dir string, used int64, info Info) string {
	var parts []string
	if info.ChainHeight > 0 && filepath.Clean(info.ChainDir) == filepath.Clean(dir) {
		parts = append(parts, "zone block "+humanize.Comma(int64(info.ChainHeight)))
	}
	parts = append(parts, humanize.Bytes(uint64(used)))
	switch home.ChainEngine(dir) {
	case "pebble":
		parts = append(parts, "Pebble")
	case "leveldb":
		parts = append(parts, "LevelDB")
	}
	return strings.Join(parts, ", ")
}

// askPort recommends another P2P port when the one set is already taken;
// otherwise it asks nothing and the port stays.
func askPort(s *settings.Settings, info Info) error {
	c := info.PortConflict
	if c == nil {
		return nil
	}
	keep := s.P2PPort
	if info.PortSuggest != 0 {
		keep = info.PortSuggest
	}
	desc := c.Problem()
	if info.PortSuggest != 0 {
		desc += fmt.Sprintf("\nPort %d is free; quai-node recommends it.", info.PortSuggest)
	}
	desc += "\nIt takes TCP and UDP; with UPnP on, go-quai asks your router to forward it."
	title := "P2P port for this node"
	if plain() {
		title += fmt.Sprintf(" (Enter for %d)", keep)
	}
	port := strconv.Itoa(keep)
	if err := ask(huh.NewGroup(
		huh.NewNote().Title("P2P port").Description(desc),
		huh.NewInput().Title(title).Value(&port).Validate(func(v string) error {
			if v = strings.TrimSpace(v); v == "" {
				return nil
			}
			if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 65535 {
				return errors.New("a port number, 1 to 65535")
			}
			return nil
		}),
	)); err != nil {
		return err
	}
	if v := strings.TrimSpace(port); v != "" {
		keep, _ = strconv.Atoi(v)
	}
	s.P2PPort = keep
	return nil
}

// askAccess asks whether other machines may open the dashboard.
func askAccess(s *settings.Settings) error {
	public := s.DashboardPublic()
	port := s.DashboardPort()
	if err := ask(huh.NewGroup(huh.NewConfirm().
		Title("Let other machines open the dashboard?").
		Description("Yes: it listens on every network interface at port " + port + ", so a phone or laptop on your network can open it. " +
			"quai-dash has no login: anyone who can reach that port sees the node's logs, its peers and its miners' addresses. Keep it to a network you trust and don't forward the port to the internet.\n" +
			"No: this machine only (http://127.0.0.1:" + port + ").").
		Affirmative("Yes, on my network").Negative("No, this machine only").
		Value(&public))); err != nil {
		return err
	}
	host := "127.0.0.1"
	if public {
		host = "0.0.0.0"
	}
	s.DashboardListen = net.JoinHostPort(host, port)
	return nil
}

// askKeepers asks whether to report to the Keepers on quainode.org, and
// under what tag.
func askKeepers(s *settings.Settings) error {
	on := s.Reporting()
	if err := ask(huh.NewGroup(
		huh.NewNote().Title("Keepers on quainode.org").Description(
			"quainode.org/nodes lists the Quai nodes that prove they keep the chain, checked every few minutes with a state proof. Reporting there means:\n"+
				"• go-quai sends its stats (heights, CPU, memory, disk) to keepers.quainode.org every 15 s, over plain HTTP, under a tag you choose.\n"+
				"• The Keepers check the node by calling its RPC, so the RPC listens on every interface and you open TCP 9200 to the internet (forward it on your router). "+
				"That makes this node a public RPC: anyone can query it or send it transactions (only the net, web3 and quai APIs: no admin, no wallet).\n"+
				"• Its P2P port, "+fmt.Sprint(s.P2PPort)+", must be reachable too.\n"+
				"Once it shows up you can claim it (quai-node keepers claim), which signs your tag with the node's key."),
		huh.NewConfirm().Title("Report this node to the Keepers on quainode.org?").
			Affirmative("Yes, report it").Negative("No").Value(&on),
	)); err != nil {
		return err
	}
	if !on {
		s.KeepersTag, s.KeepersSecret = "", ""
		return nil
	}
	tag := s.KeepersTag
	if tag == "" {
		tag = suggestTag()
	}
	def := tag
	title := "Your node's tag on quainode.org"
	if plain() {
		title += " (Enter for " + def + ")"
	}
	if err := ask(huh.NewGroup(huh.NewInput().Title(title).
		Description("3 to 24 letters, digits, '.', '_' or '-'. The first login reserves it for this node.").
		Value(&tag).
		Validate(func(v string) error {
			if strings.TrimSpace(v) == "" && plain() {
				return nil
			}
			return keepers.CheckTag(strings.TrimSpace(v))
		}))); err != nil {
		return err
	}
	if tag = strings.TrimSpace(tag); tag == "" {
		tag = def
	}
	if tag != s.KeepersTag || s.KeepersSecret == "" {
		s.KeepersSecret = keepers.NewSecret() // a new tag gets a new secret
	}
	s.KeepersTag = tag
	return nil
}

// suggestTag is a tag from the machine's hostname, if it makes one.
func suggestTag() string {
	h, _ := os.Hostname()
	h = strings.Split(h, ".")[0]
	var b strings.Builder
	for _, r := range h {
		if r < 128 && (r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	t := b.String()
	if len(t) > 24 {
		t = t[:24]
	}
	if keepers.CheckTag(t) != nil {
		return ""
	}
	return t
}

func orUnknown(s string) string {
	if s == "" {
		return "a place"
	}
	return s
}

// plain reports whether to ask line by line instead of drawing forms: when
// asked to (QUAI_NODE_PLAIN=1), or when the terminal reports no size, as
// some serial consoles and SSH setups do, which the form widgets can't lay
// out in.
func plain() bool {
	if os.Getenv("QUAI_NODE_PLAIN") != "" {
		return true
	}
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	return err != nil || w < 20
}

func serviceDescription(info Info) string {
	const what = "Installs one service, quai-node, that runs the node and the dashboard together: one start or stop covers both. It starts at boot and restarts the node if it fails."
	switch info.ServiceKind {
	case "systemd":
		return what + " Its output goes to the journal (journalctl -u quai-node). Needs sudo once."
	case "openrc":
		return what + " OpenRC writes its output to /var/log/quai-node/quai-node.log. Needs sudo once."
	case "launchd":
		return "Installs a LaunchAgent, quai-node, that runs the node and the dashboard together whenever you are logged in, and restarts the node if it fails."
	case "windows":
		return what
	}
	return ""
}

func freeNote(dir string) string {
	free, err := home.FreeBytes(dir)
	if err != nil {
		return "The chain grows over time; pick a disk with room to spare."
	}
	return fmt.Sprintf("%s free there. The chain grows over time; pick a disk with room to spare.", humanize.Bytes(free))
}

// Summary describes the settings in a few lines.
func Summary(s settings.Settings, info Info) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Network     %s, zone %s\n", s.Network, s.ZoneOf().Name())
	fmt.Fprintf(&b, "Chain data  %s\n", s.DataDir)
	if s.RestoreSnapshot {
		fmt.Fprintf(&b, "  from      quikshot.dev's snapshot, then the network (Pebble database)\n")
	} else if used := home.ChainUse(s.DataDir); used > 0 && s.WipeChain {
		fmt.Fprintf(&b, "  deletes   the chain there (%s), and syncs it from scratch\n", humanize.Bytes(uint64(used)))
	} else if used := home.ChainUse(s.DataDir); used > 0 {
		fmt.Fprintf(&b, "  keeps     the chain there (%s), and syncs on from it\n", chainHere(s.DataDir, used, info))
	}
	if s.IndexUTXOs {
		fmt.Fprintf(&b, "Index node  indexes every address's Qi UTXOs, from the first block\n")
	}
	if s.Mining {
		fmt.Fprintf(&b, "Mining      stratum on ports 3333-3335\n")
		fmt.Fprintf(&b, "  Quai to   %s\n  Qi to     %s\n", s.QuaiCoinbase, s.QiCoinbase)
	}
	if s.Dashboard {
		if s.DashboardPublic() {
			fmt.Fprintf(&b, "Dashboard   port %s on every interface (no login; keep it to a trusted network)\n", s.DashboardPort())
		} else {
			fmt.Fprintf(&b, "Dashboard   http://%s  (this machine only)\n", s.DashboardListen)
		}
		fmt.Fprintf(&b, "  Map       %s; %s\n", peersNote(s), nodeNote(s))
	}
	if s.Reporting() {
		fmt.Fprintf(&b, "Keepers     reports to keepers.quainode.org as %s; RPC public: open TCP 9200 to the internet\n", s.KeepersTag)
	}
	kind := ""
	if s.Service {
		kind = info.ServiceKind
		fmt.Fprintf(&b, "Runs as     %s service quai-node", kind)
		if s.RunAs != "" && kind != "windows" {
			fmt.Fprintf(&b, " (user %s)", s.RunAs)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("Runs        in the foreground: quai-node run\n")
	}
	fmt.Fprintf(&b, "P2P port    %d (TCP and UDP; UPnP forwarding %s)\n", s.P2PPort, onOff(s.PortMap))
	fmt.Fprintf(&b, "Logs\n%s", services.LogNote(kind, info.Home))
	return b.String()
}

func peersNote(s settings.Settings) string {
	switch s.GeoIP {
	case geo.Online:
		return "peers looked up at ip-api.com"
	case geo.DB:
		return "peers from " + s.GeoIPDB
	case geo.Off:
		return "peers not placed"
	}
	return "peers from a local database if one is found"
}

func nodeNote(s settings.Settings) string {
	if s.Location == "" {
		return "this node not placed"
	}
	return "this node at " + s.Location
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
