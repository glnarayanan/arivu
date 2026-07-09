package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/glnarayanan/arivu/internal/installer"
)

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

func TestInteractiveWizardWritesPromptsToProvidedOutput(t *testing.T) {
	input := strings.Join([]string{
		"arivu.example.com",
		"admin@example.com",
		"ops@example.com",
		"app-only",
		"n",
		"y",
		"",
	}, "\n")
	var out bytes.Buffer
	got, _, err := interactiveWizardWithIO(bufio.NewReader(strings.NewReader(input)), &out, installer.Options{}, installer.ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Domain != "arivu.example.com" || got.AdminEmail != "admin@example.com" || got.TLSEmail != "ops@example.com" || got.ProxyMode != installer.ProxyAppOnly {
		t.Fatalf("unexpected wizard options: %#v", got)
	}
	prompts := out.String()
	for _, want := range []string{"Domain/subdomain: ", "Admin email: ", "TLS notification email", "Proxy mode", "Allow public signups", "Install daily SQLite backups"} {
		if !strings.Contains(prompts, want) {
			t.Fatalf("wizard output missing %q:\n%s", want, prompts)
		}
	}
}

func TestInteractiveReconfigureKeepsDisabledBackupsOnDefault(t *testing.T) {
	opts := installer.Options{
		Domain:        "arivu.example.com",
		AdminEmail:    "admin@example.com",
		TLSEmail:      "ops@example.com",
		ProxyMode:     installer.ProxyExistingProxy,
		Reconfigure:   true,
		BackupEnabled: false,
	}
	input := strings.Join([]string{
		"", // domain
		"", // admin email
		"", // TLS email
		"", // proxy mode
		"", // signups
		"", // backups
		"",
	}, "\n")
	got, _, err := interactiveWizardWithReader(bufio.NewReader(strings.NewReader(input)), opts, installer.ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.BackupEnabled {
		t.Fatalf("default backup prompt re-enabled disabled backups: %#v", got)
	}
}

func TestCompletionMessageAvoidsPublicSuccessWhenFirewallIsManual(t *testing.T) {
	plan := installer.Plan{
		Options:     installer.Options{Domain: "arivu.example.com"},
		ProxyMode:   installer.ProxyManagedCaddy,
		BindAddress: "127.0.0.1",
		BindPort:    8090,
		Facts:       installer.HostFacts{Firewall: "ufw"},
	}
	message := completionMessage(plan)
	if strings.Contains(message, "install complete: https://") {
		t.Fatalf("message claimed public completion despite firewall blocker: %s", message)
	}
	if !strings.Contains(message, "public HTTPS still needs firewall access") || !strings.Contains(message, "sudo ufw allow 80/tcp") {
		t.Fatalf("message missing manual firewall guidance: %s", message)
	}
}

func TestCompletionMessageForAppOnlyReferencesPrintedSnippets(t *testing.T) {
	plan := installer.Plan{
		Options:     installer.Options{Domain: "arivu.example.com"},
		ProxyMode:   installer.ProxyAppOnly,
		BindAddress: "127.0.0.1",
		BindPort:    8090,
	}
	message := completionMessage(plan)
	if !strings.Contains(message, "configure your reverse proxy manually using the snippets printed above") {
		t.Fatalf("app-only completion message = %s", message)
	}
}
