package main

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sort"
	"strconv"
	"text/tabwriter"

	_ "github.com/mxk/go-sqlite/sqlite3"
	"github.com/rwcarlsen/cyan/post"
	"github.com/rwcarlsen/cyan/query"
)

var (
	help     = flag.Bool("h", false, "print this help message")
	custom   = flag.String("custom", "", "path to custom sql query spec file")
	dbname   = flag.String("db", "", "cyclus sqlite database to query")
	simidstr = flag.String("simid", "", "simulation id in hex (empty string defaults to first sim id in database")
)

var simid []byte

var command string

var db *sql.DB

var cmds = NewCmdSet()

// map[cmdname]sqltext
var customSql = map[string]string{}

func init() {
	cmds.Register("agents", "list all agents in the simulation", doAgents)
	cmds.Register("sims", "list all simulations in the database", doSims)
	cmds.Register("inv", "show inventory of one or more agents at a specific timestep", doInv)
	cmds.Register("created", "show material created by one or more agents between specific timesteps", doCreated)
	cmds.Register("deployseries", "print a time-series of a prototype's total active deployments", doDeploySeries)
	cmds.Register("flow", "Show total transacted material between two groups of agents between specific timesteps", doFlow)
	cmds.Register("invseries", "print a time series of an agent's inventory for specified isotopes", doInvSeries)
	cmds.Register("flowgraph", "print a graphviz dot graph of resource arcs between facilities", doFlowGraph)
	cmds.Register("energy", "print thermal energy (J) generated by the simulation between 2 timesteps", doEnergy)
}

func main() {
	log.SetFlags(0)
	flag.Parse()

	if *help || flag.NArg() < 1 {
		fmt.Println("Usage: metric -db <cyclus-db> [opts] <command> [args...]")
		fmt.Println("Calculates metrics for cyclus simulation data in a sqlite database.")
		flag.PrintDefaults()
		fmt.Println("\nCommands:")
		for i := range cmds.Names {
			fmt.Printf("    %v: %v\n", cmds.Names[i], cmds.Helps[i])
		}
		return
	} else if *dbname == "" {
		log.Fatal("must specify database with db flag")
	}

	if *custom != "" {
		data, err := ioutil.ReadFile(*custom)
		fatalif(err)
		fatalif(json.Unmarshal(data, &customSql))
	}

	var err error
	db, err = sql.Open("sqlite3", *dbname)
	fatalif(err)
	defer db.Close()

	if *simidstr == "" {
		ids, err := query.SimIds(db)
		fatalif(err)
		simid = ids[0]
	} else {
		simid, err = hex.DecodeString(*simidstr)
		fatalif(err)
	}

	// post process if necessary
	fatalif(post.Prepare(db))
	simids, err := post.GetSimIds(db)
	fatalif(err)
	for _, simid := range simids {
		ctx := post.NewContext(db, simid)
		err := ctx.WalkAll()
		if !post.IsAlreadyPostErr(err) {
			fatalif(err)
		}
	}
	fatalif(post.Finish(db))

	// run command
	cmds.Execute(flag.Args())
}

func doCustom(cmd string, oargs []string) {
	s, ok := customSql[cmd]
	if !ok {
		log.Fatalf("Invalid command %v", cmd)
	}
	args := make([]interface{}, len(oargs))
	for i := range args {
		args[i] = oargs[i]
	}
	rows, err := db.Query(s, args...)
	fatalif(err)

	tw := tabwriter.NewWriter(os.Stdout, 4, 4, 1, ' ', 0)
	cols, err := rows.Columns()
	fatalif(err)
	for _, c := range cols {
		_, err := tw.Write([]byte(c + "\t"))
		fatalif(err)
	}
	_, err = tw.Write([]byte("\n"))
	fatalif(err)

	for rows.Next() {
		vs := make([]interface{}, len(cols))
		vals := make([]string, len(cols))
		for i := range vals {
			vs[i] = &vals[i]
		}
		err := rows.Scan(vs...)
		fatalif(err)

		for _, v := range vals {
			_, err := tw.Write([]byte(v + "\t"))
			fatalif(err)
		}
		_, err = tw.Write([]byte("\n"))
		fatalif(err)
	}
	fatalif(rows.Err())
	fatalif(tw.Flush())
	return
}

func doSims(cmd string, args []string) {
	ids, err := query.SimIds(db)
	fatalif(err)
	for _, id := range ids {
		info, err := query.SimStat(db, id)
		fatalif(err)
		fmt.Println(info)
	}
}

func doAgents(cmd string, args []string) {
	fs := flag.NewFlagSet("agents", flag.ExitOnError)
	proto := fs.String("proto", "", "filter by prototype (default \"\" is all prototypes)")
	fs.Usage = func() {
		log.Print("Usage: agents")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	ags, err := query.AllAgents(db, simid, *proto)
	fatalif(err)
	for _, a := range ags {
		fmt.Println(a)
	}
}

func doInv(cmd string, args []string) {
	fs := flag.NewFlagSet("inv", flag.ExitOnError)
	t := fs.Int("t", -1, "timestep of inventory (-1 = end of simulation)")
	fs.Usage = func() {
		log.Print("Usage: inv [agent-id...]\nZero agents uses all agent inventories")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	var agents []int
	for _, arg := range fs.Args() {
		id, err := strconv.Atoi(arg)
		fatalif(err)
		agents = append(agents, id)
	}

	m, err := query.InvAt(db, simid, *t, agents...)
	fatalif(err)
	fmt.Printf("%+v\n", m)
}

type Row struct {
	X  int
	Ys []float64
}

type MultiSeries [][]query.XY

func (ms MultiSeries) Rows() []Row {
	rowmap := map[int]Row{}
	xs := []int{}
	for i, s := range ms {
		for _, xy := range s {
			row, ok := rowmap[xy.X]
			if !ok {
				xs = append(xs, xy.X)
				row.Ys = make([]float64, len(ms))
				row.X = xy.X
			}
			row.Ys[i] = xy.Y
			rowmap[xy.X] = row
		}
	}

	sort.Ints(xs)
	rows := make([]Row, 0, len(rowmap))
	for _, x := range xs {
		rows = append(rows, rowmap[x])
	}
	return rows
}

func doInvSeries(cmd string, args []string) {
	fs := flag.NewFlagSet("invseries", flag.ExitOnError)
	fs.Usage = func() { log.Print("Usage: invseries <agent-id> <isotope> [isotope...]"); fs.PrintDefaults() }
	fs.Parse(args)
	if fs.NArg() < 2 {
		fs.Usage()
		return
	}

	agent, err := strconv.Atoi(fs.Arg(0))
	fatalif(err)

	isos := []int{}
	for _, arg := range fs.Args()[1:] {
		iso, err := strconv.Atoi(arg)
		fatalif(err)
		isos = append(isos, iso)
	}

	ms := MultiSeries{}
	for _, iso := range isos {
		xys, err := query.InvSeries(db, simid, agent, iso)
		ms = append(ms, xys)
		fatalif(err)
	}

	fmt.Printf("# Agent %v inventory in kg\n", agent)
	fmt.Printf("# [Timestep]")
	for _, iso := range isos {
		fmt.Printf(" [%v]", iso)
	}
	fmt.Printf("\n")
	for _, row := range ms.Rows() {
		fmt.Printf("%v", row.X)
		for _, y := range row.Ys {
			fmt.Printf(" %v ", y)
		}
		fmt.Printf("\n")
	}
}

func doFlowGraph(cmd string, args []string) {
	fs := flag.NewFlagSet("flowgraph", flag.ExitOnError)
	fs.Usage = func() { log.Print("Usage: flowgraph"); fs.PrintDefaults() }
	proto := fs.Bool("proto", false, "aggregate nodes by prototype")
	t0 := fs.Int("t1", 0, "beginning of time interval (default is beginning of simulation)")
	t1 := fs.Int("t2", -1, "end of time interval (default if end of simulation)")
	fs.Parse(args)

	arcs, err := query.FlowGraph(db, simid, *t0, *t1, *proto)
	fatalif(err)

	fmt.Println("digraph ResourceFlows {")
	fmt.Println("    overlap = false;")
	fmt.Println("    nodesep=1.0;")
	fmt.Println("    edge [fontsize=9];")
	for _, arc := range arcs {
		fmt.Printf("    \"%v\" -> \"%v\" [label=\"%v\\n(%.3g kg)\"];\n", arc.Src, arc.Dst, arc.Commod, arc.Quantity)
	}
	fmt.Println("}")
}

func doDeploySeries(cmd string, args []string) {
	fs := flag.NewFlagSet("deployseries", flag.ExitOnError)
	fs.Usage = func() { log.Print("Usage: deployseries <prototype>"); fs.PrintDefaults() }
	fs.Parse(args)
	if fs.NArg() < 1 {
		fs.Usage()
		return
	}

	proto := fs.Arg(0)
	xys, err := query.DeployCumulative(db, simid, proto)
	fatalif(err)

	fmt.Printf("# Prototype %v total active deployments\n", proto)
	fmt.Println("# [Timestep] [Count]")
	for _, xy := range xys {
		fmt.Printf("%v %v\n", xy.X, xy.Y)
	}
}

func doCreated(cmd string, args []string) {
	fs := flag.NewFlagSet("created", flag.ExitOnError)
	fs.Usage = func() { log.Print("Usage: created [agent-id...]\nZero agents uses all agents"); fs.PrintDefaults() }
	t0 := fs.Int("t1", 0, "beginning of time interval (default is beginning of simulation)")
	t1 := fs.Int("t2", -1, "end of time interval (default if end of simulation)")
	fs.Parse(args)

	var agents []int

	for _, arg := range fs.Args() {
		id, err := strconv.Atoi(arg)
		fatalif(err)
		agents = append(agents, id)
	}

	m, err := query.MatCreated(db, simid, *t0, *t1, agents...)
	fatalif(err)
	fmt.Printf("%+v\n", m)
}

func doFlow(cmd string, args []string) {
	fs := flag.NewFlagSet("flow", flag.ExitOnError)
	t0 := fs.Int("t1", 0, "beginning of time interval (default is beginning of simulation)")
	t1 := fs.Int("t2", -1, "end of time interval (default if end of simulation)")
	fs.Usage = func() {
		log.Print("Usage: flow <from-agents...> .. <to-agents...>")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	var from []int
	var to []int

	if flag.NArg() < 3 {
		fs.Usage()
		return
	}

	before := true
	for _, arg := range args {
		if arg == ".." {
			before = false
			continue
		}

		id, err := strconv.Atoi(arg)
		fatalif(err)
		if before {
			from = append(from, id)
		} else {
			to = append(to, id)
		}
	}
	if len(to) < 1 {
		fs.Usage()
		return
	}

	m, err := query.Flow(db, simid, *t0, *t1, from, to)
	fatalif(err)
	fmt.Printf("%+v\n", m)
}

func doEnergy(cmd string, args []string) {
	fs := flag.NewFlagSet("energy", flag.ExitOnError)
	t0 := fs.Int("t1", 0, "beginning of time interval (default is beginning of simulation)")
	t1 := fs.Int("t2", -1, "end of time interval (default if end of simulation)")
	fs.Usage = func() {
		log.Print("Usage: energy")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	e, err := query.EnergyProduced(db, simid, *t0, *t1)
	fatalif(err)
	fmt.Println(e)
}

func fatalif(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

type CmdSet struct {
	funcs map[string]func(string, []string) // map[cmdname]func(cmdname, args)
	Names []string
	Helps []string
}

func NewCmdSet() *CmdSet {
	return &CmdSet{funcs: map[string]func(string, []string){}}
}

func (cs *CmdSet) Register(name, brief string, f func(string, []string)) {
	cs.Names = append(cs.Names, name)
	cs.Helps = append(cs.Helps, brief)
	cs.funcs[name] = f
}

func (cs *CmdSet) Execute(args []string) {
	cmd := args[0]
	f, ok := cs.funcs[cmd]
	if !ok {
		doCustom(cmd, args[1:])
		return
	}
	f(cmd, args[1:])
}
