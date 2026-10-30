package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"duplicate-detective/internal/tui"
)

const version = "1.0.0"

func main() {
	var showVersion bool
	var showHelp bool

	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.BoolVar(&showVersion, "v", false, "Print version and exit (shorthand)")
	flag.BoolVar(&showHelp, "help", false, "Print help and exit")
	flag.BoolVar(&showHelp, "h", false, "Print help and exit (shorthand)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: detective [options] [path]\n\n")
		fmt.Fprintf(os.Stderr, "Duplicate Detective: Terminal deduplication and quarantine system\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if showVersion {
		fmt.Printf("Duplicate Detective v%s\n", version)
		os.Exit(0)
	}

	if showHelp {
		flag.Usage()
		os.Exit(0)
	}

	targetPath := ""
	args := flag.Args()
	if len(args) > 0 {
		targetPath = args[0]
	}

	model := tui.NewModel(targetPath)

	// If a specific target directory was passed as a positional argument and exists,
	// immediately trigger the initial scan.
	if targetPath != "" {
		if fi, err := os.Stat(targetPath); err == nil && fi.IsDir() {
			model.AutoScan = true
		}
	}

	program := tea.NewProgram(&model, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running Duplicate Detective: %v\n", err)
		os.Exit(1)
	}
}
