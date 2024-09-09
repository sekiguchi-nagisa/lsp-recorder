package main

import (
	"fmt"
	"github.com/alecthomas/kong"
	"os"
	"runtime/debug"
)

var CLI struct {
	Bin     string   `short:"b" required:"" help:"Specify language server executable"`
	Version bool     `short:"v" help:"Show version info"`
	Args    []string `arg:"" optional:"" help:"additional options/arguments for language server"`
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
}
