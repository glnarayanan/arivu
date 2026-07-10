package buildinfo

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

var readBuildInfo = debug.ReadBuildInfo
var releaseVersion string

func Version() string {
	if version := strings.TrimSpace(releaseVersion); version != "" {
		return version
	}
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
