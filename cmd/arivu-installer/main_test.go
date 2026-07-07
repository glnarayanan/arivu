package main

import "testing"

func TestParseOptionsAllowsNonInteractivePlanWithoutPassword(t *testing.T) {
	_, _, nonInteractive, _, err := parseOptions([]string{
		"--non-interactive",
		"--domain", "arivu.example.com",
		"--admin-email", "admin@example.com",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !nonInteractive {
		t.Fatal("expected non-interactive mode")
	}
}

func TestParseOptionsRequiresPasswordForNonInteractiveInstall(t *testing.T) {
	_, _, _, _, err := parseOptions([]string{
		"--non-interactive",
		"--domain", "arivu.example.com",
		"--admin-email", "admin@example.com",
	}, true)
	if err == nil {
		t.Fatal("expected missing password file error")
	}
}

func TestParseOptionsAllowsDryRunInstallWithoutPassword(t *testing.T) {
	_, apply, _, _, err := parseOptions([]string{
		"--non-interactive",
		"--dry-run",
		"--domain", "arivu.example.com",
		"--admin-email", "admin@example.com",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !apply.DryRun {
		t.Fatal("expected dry-run mode")
	}
}
