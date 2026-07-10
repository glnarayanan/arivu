package buildinfo

import (
	"fmt"
	"io"
	"runtime/debug"
)

var readBuildInfo = debug.ReadBuildInfo

func Version() string {
	info, ok := readBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "devel"
	}
	return info.Main.Version
}

func WriteIfRequested(w io.Writer, name string, args []string) bool {
	if len(args) == 0 || (args[0] != "version" && args[0] != "--version") {
		return false
	}
	fmt.Fprintf(w, "%s %s\n", name, Version())
	return true
}
