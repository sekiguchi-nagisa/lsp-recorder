package main

import (
	"fmt"
	"github.com/alecthomas/kong"
	"log/slog"
	"os"
	"runtime/debug"
)

var CLI struct {
	Record struct {
		Log    string   `optional:"" default:"./lsp-recorder.log" help:"Log file path"`
		Format string   `optional:"" enum:"text,json" default:"text" help:"Log file format"`
		Bin    string   `arg:"" required:"" help:"Language Server executable path"`
		Args   []string `arg:"" optional:"" help:"Additional options/arguments of Language Server"`
	} `cmd:"" help:"Run and record Language Server"`

	Version kong.VersionFlag `short:"v" help:"Show version information"`
}

var version = "" // for version embedding (specified like "-X main.version=v0.1.0")

func getVersion() string {
	info, ok := debug.ReadBuildInfo()
	if ok {
		rev := "unknown"
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				rev = setting.Value
				break
			}
		}
		var v = info.Main.Version
		if version != "" { // set by "-X main.version=v0.1.0"
			v = version
		}
		return fmt.Sprintf("%s (%s)", v, rev)
	} else {
		return "(unknown)"
	}
}

func main() {
	ctx := kong.Parse(&CLI, kong.UsageOnError(), kong.Vars{"version": getVersion()})
	switch ctx.Command() {
	case "record <bin>":
		logFile, err := os.Create(CLI.Record.Log)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "cannot open log file: %s, caused by %s\n", CLI.Record.Log, err.Error())
			os.Exit(1)
		}
		defer func(logFile *os.File) {
			_ = logFile.Close()
		}(logFile)

		var handler slog.Handler
		switch CLI.Record.Format {
		case "text":
			handler = slog.NewTextHandler(logFile, nil)
		case "json":
			handler = slog.NewJSONHandler(logFile, nil)
		default:
			panic("unknown format: " + CLI.Record.Format)
		}
		Run(CLI.Record.Bin, CLI.Record.Args, slog.New(handler))
	default:
		panic("unknown command: " + ctx.Command())
	}
}
