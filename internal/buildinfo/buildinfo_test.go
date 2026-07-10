package buildinfo

import (
	"bytes"
	"runtime/debug"
	"testing"
)

func TestWriteIfRequestedSupportsCommandAndFlag(t *testing.T) {
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true
	}
	t.Cleanup(func() { readBuildInfo = old })

	for _, arg := range []string{"version", "--version"} {
		var output bytes.Buffer
		if !WriteIfRequested(&output, "arivu", []string{arg}) {
			t.Fatalf("%s was not recognized as a version request", arg)
		}
		if got := output.String(); got != "arivu v1.2.3\n" {
			t.Fatalf("version output = %q", got)
		}
	}
}

func TestWriteIfRequestedIgnoresOtherCommands(t *testing.T) {
	var output bytes.Buffer
	if WriteIfRequested(&output, "arivu", []string{"serve"}) {
		t.Fatal("serve was treated as a version request")
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected output %q", output.String())
	}
}

func TestVersionFallsBackForDevelopmentBuilds(t *testing.T) {
	oldVersion := releaseVersion
	releaseVersion = ""
	t.Cleanup(func() { releaseVersion = oldVersion })
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true
	}
	t.Cleanup(func() { readBuildInfo = old })

	if got := Version(); got != "devel" {
		t.Fatalf("Version() = %q", got)
	}
}

func TestVersionPrefersInjectedReleaseVersion(t *testing.T) {
	oldVersion := releaseVersion
	releaseVersion = " v1.2.3 "
	t.Cleanup(func() { releaseVersion = oldVersion })

	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true
	}
	t.Cleanup(func() { readBuildInfo = old })

	if got := Version(); got != "v1.2.3" {
		t.Fatalf("Version() = %q", got)
	}
}
