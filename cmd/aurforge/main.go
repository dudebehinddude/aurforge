package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/dudebehinddude/aurforge/ent"
	"github.com/dudebehinddude/aurforge/internal"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := internal.LoadConfig()
	if err != nil {
		fatal(err)
	}
	db, err := internal.OpenDB(ctx, cfg)
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	switch os.Args[1] {
	case "controller":
		if err := internal.RunController(ctx, db); err != nil && !errors.Is(err, context.Canceled) {
			fatal(err)
		}
	case "scheduler":
		if err := internal.RunScheduler(ctx, cfg, db, logger); err != nil {
			fatal(err)
		}
	case "worker":
		if err := internal.RunWorker(ctx, cfg, db, logger); err != nil {
			fatal(err)
		}
	case "publisher":
		if err := internal.RunPublisher(ctx, cfg, db, logger); err != nil {
			fatal(err)
		}
	case "add":
		add(ctx, cfg, db, os.Args[2:])
	case "update":
		add(ctx, cfg, db, os.Args[2:])
	case "status":
		status(ctx, db)
	default:
		usage()
		os.Exit(2)
	}
}

func add(ctx context.Context, cfg internal.Config, db *ent.Client, args []string) {
	if len(args) == 0 {
		fatal(fmt.Errorf("add requires an AUR query or --local path"))
	}
	yes, local, selectIndex := false, "", -1
	var queryParts []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--yes", "-y":
			yes = true
		case "--local", "-l":
			index++
			if index >= len(args) {
				fatal(fmt.Errorf("--local requires a path"))
			}
			local = args[index]
		case "--select":
			index++
			if index >= len(args) {
				fatal(fmt.Errorf("--select requires an index"))
			}
			value, err := strconv.Atoi(args[index])
			if err != nil {
				fatal(err)
			}
			selectIndex = value
		default:
			queryParts = append(queryParts, args[index])
		}
	}
	reader := bufio.NewReader(os.Stdin)
	if local != "" {
		preview, err := internal.PreviewLocal(cfg, local)
		if err != nil {
			fatal(err)
		}
		showPreview(preview)
		ok, err := internal.PromptConfirm(reader, os.Stdout, "Import this local package?", yes)
		if err != nil {
			fatal(err)
		}
		if !ok {
			return
		}
		id, err := internal.AcceptLocal(ctx, cfg, db, preview)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Imported %s as package version %d; build eligible after the configured delay.\n", preview.Package.Name, id)
		return
	}
	query := strings.Join(queryParts, " ")
	if query == "" {
		fatal(fmt.Errorf("AUR query is required"))
	}
	results, err := internal.SearchAUR(ctx, query)
	if err != nil {
		fatal(err)
	}
	if len(results) == 0 {
		fatal(fmt.Errorf("no AUR packages matched %q", query))
	}
	if selectIndex < 0 {
		for index, result := range results {
			fmt.Printf("%d) %s %s - %s\n", index+1, result.Name, result.Version, result.Description)
		}
		fmt.Fprint(os.Stdout, "Select package number: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			fatal(err)
		}
		selectIndex, err = strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			fatal(err)
		}
	}
	if selectIndex < 1 || selectIndex > len(results) {
		fatal(fmt.Errorf("selection %d is out of range", selectIndex))
	}
	selected := results[selectIndex-1]
	graph, err := internal.ResolveAURGraph(ctx, cfg, selected.Name)
	if err != nil {
		fatal(err)
	}
	preview := graph[0].Preview
	for _, node := range graph {
		showPreview(node.Preview)
	}
	fmt.Printf("AUR dependency graph contains %d package(s).\n", len(graph))
	ok, err := internal.PromptConfirm(reader, os.Stdout, "Import this AUR package?", yes)
	if err != nil {
		fatal(err)
	}
	if !ok {
		return
	}
	var rootID int
	for _, node := range graph {
		id, err := internal.AcceptAUR(ctx, cfg, db, node.Preview, node.Revision)
		if err != nil {
			fatal(err)
		}
		if node.Preview.Package.Name == preview.Package.Name {
			rootID = id
		}
	}
	fmt.Printf("Imported %s and %d dependency package(s); root package version %d is eligible after the configured delay.\n", preview.Package.Name, len(graph)-1, rootID)
}

func status(ctx context.Context, db *ent.Client) {
	packages, err := internal.ListPackages(ctx, db)
	if err != nil {
		fatal(err)
	}
	for _, packageStatus := range packages {
		fmt.Println(packageStatus)
	}
}

func showPreview(preview internal.Preview) {
	pkg := preview.Package
	fmt.Printf("\nPackage: %s %s-%s\n", pkg.Name, pkg.Version, pkg.Release)
	fmt.Printf("Source: %s\n", pkg.SourceKind)
	fmt.Printf("Snapshot: %s\n", preview.Digest)
	fmt.Printf("Split packages: %s\n", strings.Join(pkg.SplitPackages, ", "))
	if len(pkg.Dependencies) > 0 {
		names := make([]string, 0, len(pkg.Dependencies))
		for _, dep := range pkg.Dependencies {
			names = append(names, dep.Name)
		}
		fmt.Printf("Declared dependencies: %s\n", strings.Join(names, ", "))
	}
	for _, warning := range append(pkg.Warnings, preview.Audit...) {
		fmt.Printf("Warning: %s\n", warning)
	}
	if len(pkg.Warnings) == 0 && len(preview.Audit) == 0 {
		fmt.Println("Audit: no static policy warnings")
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: aurforge <controller|scheduler|worker|publisher|add|update|status>")
	fmt.Fprintln(os.Stderr, "       aurforge add <aur-query> [--select N] [--yes]")
	fmt.Fprintln(os.Stderr, "       aurforge add --local <package> [--yes]")
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "aurforge:", err); os.Exit(1) }
