package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"layercake/internal/layercake"
)

var (
	imagesDir string
	verbose   bool
)

func main() {
	// Find images directory relative to executable or current directory
	defaultImagesDir := findImagesDir()

	flag.StringVar(&imagesDir, "images", defaultImagesDir, "Path to images directory")
	flag.BoolVar(&verbose, "v", false, "Verbose output")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: layercake [options] <command> [args]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  build [--all] [--force] [--cascade] [layer-id]  Build layer(s)\n")
		fmt.Fprintf(os.Stderr, "  list                                            List all layers\n")
		fmt.Fprintf(os.Stderr, "  status                                          Show build status\n")
		fmt.Fprintf(os.Stderr, "  export <sandfire-data-dir>                      Export to sandfire\n")
		fmt.Fprintf(os.Stderr, "\nOptions:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	cmd := flag.Arg(0)
	args := flag.Args()[1:]

	if err := run(cmd, args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func findImagesDir() string {
	// Try relative to executable
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Join(filepath.Dir(exe), "..", "images")
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}

	// Try relative to current directory
	if _, err := os.Stat("images"); err == nil {
		return "images"
	}

	// Default
	return "./images"
}

func run(cmd string, args []string) error {
	switch cmd {
	case "build":
		return cmdBuild(args)
	case "list":
		return cmdList(args)
	case "status":
		return cmdStatus(args)
	case "export":
		return cmdExport(args)
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func loadGraph() (*layercake.LayerGraph, error) {
	absImagesDir, err := filepath.Abs(imagesDir)
	if err != nil {
		return nil, err
	}

	layers, err := layercake.LoadAllLayers(absImagesDir)
	if err != nil {
		return nil, err
	}

	if len(layers) == 0 {
		return nil, fmt.Errorf("no layers found in %s", absImagesDir)
	}

	return layercake.NewLayerGraph(layers)
}

func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	all := fs.Bool("all", false, "Build all layers")
	force := fs.Bool("force", false, "Force rebuild even if up to date")
	cascade := fs.Bool("cascade", false, "Rebuild layer and all descendants")
	fs.Parse(args)

	graph, err := loadGraph()
	if err != nil {
		return err
	}

	builder := layercake.NewBuilder(graph, verbose)

	if *all {
		return builder.BuildAll(*force)
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("specify a layer ID or use --all")
	}

	layerID := fs.Arg(0)
	if *cascade {
		return builder.BuildCascade(layerID)
	}

	return builder.Build(layerID, *force)
}

func cmdList(args []string) error {
	graph, err := loadGraph()
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tPARENT\tEXPORT")
	fmt.Fprintln(w, "--\t----\t------\t------")

	for _, layer := range graph.TopologicalSort() {
		export := ""
		if layer.Export {
			export = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", layer.ID, layer.Name, layer.Parent, export)
	}

	return w.Flush()
}

func cmdStatus(args []string) error {
	graph, err := loadGraph()
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tREASON")
	fmt.Fprintln(w, "--\t------\t------")

	for _, layer := range graph.TopologicalSort() {
		status, err := layercake.GetBuildStatus(layer, graph)
		if err != nil {
			fmt.Fprintf(w, "%s\terror\t%v\n", layer.ID, err)
			continue
		}

		statusStr := "up-to-date"
		reason := ""
		if !status.Built {
			statusStr = "not built"
			reason = status.StaleReason
		} else if status.Stale {
			statusStr = "stale"
			reason = status.StaleReason
		}

		fmt.Fprintf(w, "%s\t%s\t%s\n", layer.ID, statusStr, reason)
	}

	return w.Flush()
}

func cmdExport(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: layercake export <sandfire-data-dir>")
	}

	sandfireDir, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}

	// Verify sandfire directory exists
	dbPath := filepath.Join(sandfireDir, "sandfire.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("sandfire database not found at %s", dbPath)
	}

	graph, err := loadGraph()
	if err != nil {
		return err
	}

	exporter := layercake.NewExporter(graph, sandfireDir)
	return exporter.Export()
}
