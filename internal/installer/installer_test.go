package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestBuildPlanCleanHostUsesManagedCaddy(t *testing.T) {
	plan, err := BuildPlan(baseOptions(), cleanFacts())
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProxyMode != ProxyManagedCaddy {
		t.Fatalf("proxy mode = %s", plan.ProxyMode)
	}
	if plan.BindPort != 8090 {
		t.Fatalf("bind port = %d", plan.BindPort)
	}
	if !hasFile(plan, "/etc/caddy/conf.d/arivu.caddy") {
		t.Fatalf("managed caddy plan missing Caddy site file: %#v", plan.Files)
	}
	if !strings.Contains(fileContent(plan, "/etc/caddy/conf.d/arivu.caddy"), "\ttls ops@example.com\n") {
		t.Fatalf("managed caddy plan missing TLS email: %q", fileContent(plan, "/etc/caddy/conf.d/arivu.caddy"))
	}
	if !strings.Contains(FormatPlan(plan), "Arivu install plan for arivu.example.com") {
		t.Fatalf("formatted plan missing domain")
	}
	if !strings.Contains(FormatPlan(plan), "Release: latest") {
		t.Fatalf("formatted plan missing release")
	}
}

func TestBuildPlanNormalizesEmailAddresses(t *testing.T) {
	opts := baseOptions()
	opts.AdminEmail = "Admin <ADMIN@EXAMPLE.COM>"
	opts.TLSEmail = "Ops <OPS@EXAMPLE.COM>"
	plan, err := BuildPlan(opts, cleanFacts())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fileContent(plan, "/etc/arivu/arivu.env"), "ADMIN_EMAILS=admin@example.com") {
		t.Fatalf("env did not normalize admin email: %q", fileContent(plan, "/etc/arivu/arivu.env"))
	}
	if !strings.Contains(fileContent(plan, "/etc/caddy/conf.d/arivu.caddy"), "\ttls ops@example.com\n") {
		t.Fatalf("caddy did not normalize TLS email: %q", fileContent(plan, "/etc/caddy/conf.d/arivu.caddy"))
	}
}

func TestBuildPlanSharedHostUsesExistingProxy(t *testing.T) {
	facts := cleanFacts()
	facts.Commands["nginx"] = "/usr/sbin/nginx"
	facts.Listeners[80] = "nginx"
	facts.Listeners[443] = "nginx"
	plan, err := BuildPlan(baseOptions(), facts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ProxyMode != ProxyExistingProxy {
		t.Fatalf("proxy mode = %s", plan.ProxyMode)
	}
	if plan.BindPort != 8090 {
		t.Fatalf("bind port = %d", plan.BindPort)
	}
	if len(plan.Warnings) == 0 {
		t.Fatal("expected shared-host warning")
	}
	if !hasFile(plan, "/etc/nginx/snippets/arivu.conf") {
		t.Fatalf("existing proxy plan missing nginx snippet: %#v", plan.Files)
	}
	if hasFile(plan, "/etc/apache2/conf-available/arivu.conf") || hasFile(plan, "/etc/arivu/proxy/Caddyfile.arivu") {
		t.Fatalf("nginx existing-proxy plan should not manage other proxy configs: %#v", plan.Files)
	}
}

func TestBuildPlanAppOnlySkipsProxyFiles(t *testing.T) {
	opts := baseOptions()
	opts.ProxyMode = ProxyAppOnly
	plan, err := BuildPlan(opts, cleanFacts())
	if err != nil {
		t.Fatal(err)
	}
	if hasFile(plan, "/etc/caddy/conf.d/arivu.caddy") || hasFile(plan, "/etc/nginx/snippets/arivu.conf") {
		t.Fatalf("app-only should not manage proxy files: %#v", plan.Files)
	}
}

func TestBuildPlanBackupsDisabledSkipsBackupUnits(t *testing.T) {
	opts := baseOptions()
	opts.BackupEnabled = false
	plan, err := BuildPlan(opts, cleanFacts())
	if err != nil {
		t.Fatal(err)
	}
	if hasFile(plan, "/etc/systemd/system/arivu-backup.service") || hasFile(plan, "/etc/systemd/system/arivu-backup.timer") {
		t.Fatalf("backups disabled should not manage backup units: %#v", plan.Files)
	}
}

func TestBuildPlanRejectsExistingDomainVHost(t *testing.T) {
	facts := cleanFacts()
	facts.ExistingVHosts = []string{"arivu.example.com"}
	if _, err := BuildPlan(baseOptions(), facts); err == nil {
		t.Fatal("expected existing domain vhost to fail")
	}
}

func TestVerifyChecksumRejectsTamperedArtifact(t *testing.T) {
	data := []byte("binary")
	sum := sha256.Sum256(data)
	sums := []byte(hex.EncodeToString(sum[:]) + "  arivu-linux-amd64\n")
	if err := VerifyChecksum(data, sums, "arivu-linux-amd64"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksum([]byte("tampered"), sums, "arivu-linux-amd64"); err == nil {
		t.Fatal("expected tampered checksum to fail")
	}
}

func TestReleaseArtifactURLsSupportPinnedVersions(t *testing.T) {
	appURL, sumsURL := ReleaseArtifactURLs("https://github.com/glnarayanan/arivu", "v1.2.3", "arm64")
	if appURL != "https://github.com/glnarayanan/arivu/releases/download/v1.2.3/arivu-linux-arm64" {
		t.Fatalf("versioned app URL = %s", appURL)
	}
	if sumsURL != "https://github.com/glnarayanan/arivu/releases/download/v1.2.3/SHA256SUMS" {
		t.Fatalf("versioned sums URL = %s", sumsURL)
	}
	latestURL, _ := ReleaseArtifactURLs("https://github.com/glnarayanan/arivu", "", "amd64")
	if latestURL != "https://github.com/glnarayanan/arivu/releases/latest/download/arivu-linux-amd64" {
		t.Fatalf("latest app URL = %s", latestURL)
	}
}

func TestOptionsFromEnvFileLoadsReconfigureDefaults(t *testing.T) {
	path := t.TempDir() + "/arivu.env"
	if err := os.WriteFile(path, []byte(strings.Join([]string{
		"ARIVU_ADDR=127.0.0.1:8123",
		"APP_URL=https://arivu.example.net",
		"SIGNUPS_ENABLED=true",
		"ADMIN_EMAILS=admin@example.net,ops@example.net",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := OptionsFromEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Domain != "arivu.example.net" || opts.AdminEmail != "admin@example.net" || opts.BindPort != 8123 || !opts.SignupsEnabled {
		t.Fatalf("unexpected env options: %#v", opts)
	}
}

func baseOptions() Options {
	return Options{
		Domain:         "arivu.example.com",
		AdminEmail:     "admin@example.com",
		TLSEmail:       "ops@example.com",
		ProxyMode:      ProxyAuto,
		BackupEnabled:  true,
		SkipDNSCheck:   true,
		SignupsEnabled: false,
	}
}

func cleanFacts() HostFacts {
	return HostFacts{
		OSID:        "ubuntu",
		OSVersionID: "24.04",
		Arch:        "amd64",
		HasSystemd:  true,
		Commands:    map[string]string{"apt-get": "/usr/bin/apt-get"},
		Listeners:   map[int]string{},
	}
}

func hasFile(plan Plan, path string) bool {
	for _, file := range plan.Files {
		if file.Path == path {
			return true
		}
	}
	return false
}

func fileContent(plan Plan, path string) string {
	for _, file := range plan.Files {
		if file.Path == path {
			return file.Content
		}
	}
	return ""
}
