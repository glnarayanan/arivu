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

func TestBuildPlanAllowsFutureUbuntuVersions(t *testing.T) {
	facts := cleanFacts()
	facts.OSVersionID = "26.04"
	if _, err := BuildPlan(baseOptions(), facts); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPlanWarnsOnDNSMismatch(t *testing.T) {
	opts := baseOptions()
	opts.SkipDNSCheck = false
	facts := cleanFacts()
	facts.PublicIP = "51.210.96.239"
	facts.DomainIPs = []string{"104.21.1.1", "172.67.1.1"}
	plan, err := BuildPlan(opts, facts)
	if err != nil {
		t.Fatal(err)
	}
	formatted := FormatPlan(plan)
	for _, expected := range []string{"currently resolves to 104.21.1.1, 172.67.1.1", "not this server IP 51.210.96.239", "Cloudflare proxy", "sudo arivu-installer reconfigure --domain arivu.example.com"} {
		if !strings.Contains(formatted, expected) {
			t.Fatalf("DNS warning missing %q:\n%s", expected, formatted)
		}
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
	if !strings.Contains(fileContent(plan, "/etc/arivu/arivu.env"), "ARIVU_INSTALLER_PROXY_MODE=managed-caddy") {
		t.Fatalf("env did not record resolved proxy mode: %q", fileContent(plan, "/etc/arivu/arivu.env"))
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
	formatted := FormatPlan(plan)
	for _, expected := range []string{"Manual proxy snippets:", "Caddy:", "reverse_proxy 127.0.0.1:8090", "Nginx:", "proxy_pass http://127.0.0.1:8090", "Apache:", "ProxyPass / http://127.0.0.1:8090/"} {
		if !strings.Contains(formatted, expected) {
			t.Fatalf("app-only plan missing %q:\n%s", expected, formatted)
		}
	}
}

func TestManagedCaddyPlanWithFirewallPrintsManualCommands(t *testing.T) {
	facts := cleanFacts()
	facts.Commands["ufw"] = "/usr/sbin/ufw"
	facts.Firewall = "ufw"
	plan, err := BuildPlan(baseOptions(), facts)
	if err != nil {
		t.Fatal(err)
	}
	if !RequiresManualFirewall(plan) {
		t.Fatalf("expected manual firewall requirement: %#v", plan)
	}
	formatted := FormatPlan(plan)
	if !strings.Contains(formatted, "Manual firewall commands required") || !strings.Contains(formatted, "sudo ufw allow 80/tcp") || !strings.Contains(formatted, "sudo ufw allow 443/tcp") {
		t.Fatalf("managed-caddy firewall plan missing manual commands:\n%s", formatted)
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

func TestBuildPlanAllowsOwnVHostDuringReconfigure(t *testing.T) {
	opts := baseOptions()
	opts.Reconfigure = true
	facts := cleanFacts()
	facts.EtcExists = true
	facts.ExistingVHosts = []string{"https://arivu.example.com"}
	if _, err := BuildPlan(opts, facts); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPlanRejectsUnsafeDomains(t *testing.T) {
	for _, domain := range []string{
		"https://arivu.example.com",
		"arivu.example.com:443",
		"arivu.example.com/foo",
		"arivu.example.com {",
		"arivu.example.com\nexample.net",
		"-bad.example.com",
	} {
		opts := baseOptions()
		opts.Domain = domain
		if _, err := BuildPlan(opts, cleanFacts()); err == nil {
			t.Fatalf("expected %q to fail", domain)
		}
	}
}

func TestCaddyHostsFromLineParsesSiteLabels(t *testing.T) {
	hosts := caddyHostsFromLine("https://arivu.example.com, www.example.com {")
	if len(hosts) != 2 || hosts[0] != "arivu.example.com" || hosts[1] != "www.example.com" {
		t.Fatalf("unexpected hosts: %#v", hosts)
	}
	if hosts := caddyHostsFromLine("reverse_proxy 127.0.0.1:8090 {"); len(hosts) != 0 {
		t.Fatalf("directive should not be parsed as hosts: %#v", hosts)
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
	appURL, installerURL, sumsURL := ReleaseArtifactURLs("https://github.com/glnarayanan/arivu", "v1.2.3", "arm64")
	if appURL != "https://github.com/glnarayanan/arivu/releases/download/v1.2.3/arivu-linux-arm64" {
		t.Fatalf("versioned app URL = %s", appURL)
	}
	if installerURL != "https://github.com/glnarayanan/arivu/releases/download/v1.2.3/arivu-installer-linux-arm64" {
		t.Fatalf("versioned installer URL = %s", installerURL)
	}
	if sumsURL != "https://github.com/glnarayanan/arivu/releases/download/v1.2.3/SHA256SUMS" {
		t.Fatalf("versioned sums URL = %s", sumsURL)
	}
	latestURL, latestInstallerURL, _ := ReleaseArtifactURLs("https://github.com/glnarayanan/arivu", "", "amd64")
	if latestURL != "https://github.com/glnarayanan/arivu/releases/latest/download/arivu-linux-amd64" {
		t.Fatalf("latest app URL = %s", latestURL)
	}
	if latestInstallerURL != "https://github.com/glnarayanan/arivu/releases/latest/download/arivu-installer-linux-amd64" {
		t.Fatalf("latest installer URL = %s", latestInstallerURL)
	}
}

func TestOptionsFromEnvFileLoadsReconfigureDefaults(t *testing.T) {
	path := t.TempDir() + "/arivu.env"
	if err := os.WriteFile(path, []byte(strings.Join([]string{
		"ARIVU_ADDR=127.0.0.1:8123",
		"APP_URL=https://arivu.example.net",
		"SIGNUPS_ENABLED=true",
		"ADMIN_EMAILS=admin@example.net,ops@example.net",
		"ARIVU_INSTALLER_VERSION=v1.2.3",
		"ARIVU_INSTALLER_PROXY_MODE=existing-proxy",
		"ARIVU_TLS_EMAIL=ops@example.net",
		"ARIVU_BACKUPS_ENABLED=false",
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
	if opts.Version != "v1.2.3" || opts.ProxyMode != ProxyExistingProxy || opts.TLSEmail != "ops@example.net" || opts.BackupEnabled {
		t.Fatalf("unexpected installer env options: %#v", opts)
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
