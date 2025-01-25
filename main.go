package main

import (
	"bytes"
	"fmt"
	"github.com/alecthomas/kong"
	"log/slog"
	"os"
	"runtime/debug"
)

type CLIRecord struct {
	Log    string   `optional:"" default:"./lsp-recorder.log" help:"Log file path"`
	Format string   `optional:"" enum:"text,json" default:"text" help:"Log file format"`
	Bin    string   `arg:"" required:"" help:"Language Server executable path"`
	Args   []string `arg:"" optional:"" help:"Additional options/arguments of Language Server"`
}

type CLIPrint struct {
	Log string `arg:"" required:"" help:"Log file path"`
}

var CLI struct {
	Record CLIRecord `cmd:"" help:"Run and record Language Server"`

	Print CLIPrint `cmd:"" help:"Pretty print log"`

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

func (r *CLIRecord) Run() error {
	logFile, err := os.Create(r.Log)
	if err != nil {
		return fmt.Errorf("cannot open log file: %s, caused by %s\n", r.Log, err.Error())
	}
	defer func(logFile *os.File) {
		_ = logFile.Close()
	}(logFile)

	var handler slog.Handler
	switch r.Format {
	case "text":
		handler = slog.NewTextHandler(logFile, nil)
	case "json":
		handler = slog.NewJSONHandler(logFile, nil)
	default:
		panic("unknown format: " + r.Format)
	}
	Run(r.Bin, r.Args, slog.New(handler))
	return nil
}

func (p *CLIPrint) Run() error {
	buf, err := os.ReadFile(p.Log)
	if err != nil {
		return fmt.Errorf("cannot open log file: %s, caused by %s\n", p.Log, err.Error())
	}
	reader := bytes.NewReader(buf)
	err = Print(reader, os.Stdout)
	if err != nil {
		return fmt.Errorf("cannot print log: %s, caused by %s\n", p.Log, err.Error())
	}
	return nil
}

func main() {
	ctx := kong.Parse(&CLI, kong.UsageOnError(), kong.Vars{"version": getVersion()})
	err := ctx.Run()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
