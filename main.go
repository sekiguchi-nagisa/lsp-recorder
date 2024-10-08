package main

import (
	"fmt"
	"github.com/alecthomas/kong"
	"os"
	"runtime/debug"
)

var CLI struct {
	Version bool     `short:"v" help:"Show version info"`
	Log     string   `optional:"" default:"./lsp-recorder.log" help:"Log file path"`
	Bin     string   `arg:"" required:"" help:"Language Server executable path"`
	Args    []string `arg:"" optional:"" help:"Additional options/arguments of Language Server"`
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
	kong.Parse(&CLI, kong.UsageOnError())
	if CLI.Version {
		fmt.Println(getVersion())
		os.Exit(0)
	}

	logFile, err := os.Create(CLI.Log)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "cannot open log file: %s, caused by %s\n", CLI.Log, err.Error())
		os.Exit(1)
	}
	defer func(logFile *os.File) {
		_ = logFile.Close()
	}(logFile)

	Run(CLI.Bin, CLI.Args, logFile)
}
