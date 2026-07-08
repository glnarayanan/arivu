package main

import "testing"

func TestParseOptionsAllowsNonInteractivePlanWithoutPassword(t *testing.T) {
	_, _, nonInteractive, _, _, err := parseOptions([]string{
		"--non-interactive",
		"--domain", "arivu.example.com",
		"--admin-email", "admin@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !nonInteractive {
		t.Fatal("expected non-interactive mode")
	}
}

func TestParseOptionsRequiresPasswordForNonInteractiveInstall(t *testing.T) {
	opts, apply, nonInteractive, _, _, err := parseOptions([]string{
		"--non-interactive",
		"--domain", "arivu.example.com",
		"--admin-email", "admin@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = validateInstallOptions(opts, apply, nonInteractive, true)
	if err == nil {
		t.Fatal("expected missing password file error")
	}
}

func TestParseOptionsAllowsDryRunInstallWithoutPassword(t *testing.T) {
	opts, apply, nonInteractive, _, _, err := parseOptions([]string{
		"--non-interactive",
		"--dry-run",
		"--domain", "arivu.example.com",
		"--admin-email", "admin@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateInstallOptions(opts, apply, nonInteractive, true); err != nil {
		t.Fatal(err)
	}
	if !apply.DryRun {
		t.Fatal("expected dry-run mode")
	}
}

func TestParseOptionsAcceptsExistingProxyAliasAndVersion(t *testing.T) {
	opts, _, _, _, flagsSet, err := parseOptions([]string{
		"--domain", "arivu.example.com",
		"--admin-email", "admin@example.com",
		"--proxy-mode", "existing",
		"--version", "v1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.ProxyMode != "existing-proxy" {
		t.Fatalf("proxy mode = %s", opts.ProxyMode)
	}
	if opts.Version != "v1.2.3" {
		t.Fatalf("version = %q", opts.Version)
	}
	if !flagsSet["version"] || !flagsSet["proxy-mode"] {
		t.Fatalf("missing visited flags: %#v", flagsSet)
	}
}

func TestValidateAllowsNonInteractiveReconfigureWithoutPassword(t *testing.T) {
	opts, apply, nonInteractive, _, _, err := parseOptions([]string{
		"--non-interactive",
		"--reconfigure",
		"--domain", "arivu.example.com",
		"--admin-email", "admin@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateInstallOptions(opts, apply, nonInteractive, true); err != nil {
		t.Fatal(err)
	}
}
