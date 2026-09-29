package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestBuildPlanRequiresSupportedCaptureHost(t *testing.T) {
	for _, test := range []struct {
		osID    string
		version string
	}{
		{osID: "ubuntu", version: "20.04"},
		{osID: "debian", version: "11"},
	} {
		t.Run(test.osID+"_"+test.version, func(t *testing.T) {
			opts := baseOptions()
			opts.CaptureEnabled = true
			facts := cleanFacts()
			facts.OSID = test.osID
			facts.OSVersionID = test.version
			if _, err := BuildPlan(opts, facts); err == nil || !strings.Contains(err.Error(), "--browser-capture=false") {
				t.Fatalf("capture host validation error = %v", err)
			}
			opts.CaptureEnabled = false
			if _, err := BuildPlan(opts, facts); err != nil {
				t.Fatalf("core-only install rejected: %v", err)
			}
		})
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

func TestBuildPlanCaptureEnabledManagesNativeServiceAndEnvironment(t *testing.T) {
	opts := baseOptions()
	opts.CaptureEnabled = true
	plan, err := BuildPlan(opts, cleanFacts())
	if err != nil {
		t.Fatal(err)
	}
	if !hasFile(plan, "/etc/systemd/system/arivu-capture.service") {
		t.Fatalf("capture-enabled plan missing native service: %#v", plan.Files)
	}
	service := fileContent(plan, "/etc/systemd/system/arivu-capture.service")
	for _, expected := range []string{
		"ExecStart=/usr/local/lib/arivu-capture/node src/index.mjs",
		"User=arivu-capture",
		"PrivateNetwork=true",
		"NoNewPrivileges=true",
	} {
		if !strings.Contains(service, expected) {
			t.Fatalf("capture service missing %q: %s", expected, service)
		}
	}
	env := fileContent(plan, "/etc/arivu/arivu.env")
	for _, expected := range []string{
		"ARIVU_BROWSER_CAPTURE_ENABLED=true",
		"ARIVU_BROWSER_CAPTURE_PROTOCOL=2",
		"ARIVU_BROWSER_CAPTURE_SOCKET=/run/arivu-capture/helper.sock",
		"ARIVU_BROWSER_CAPTURE_SELF_CONTAINED_HTML=true",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("capture env missing %q: %s", expected, env)
		}
	}
	if !strings.Contains(FormatPlan(plan), "Install complete capture") {
		t.Fatalf("capture action missing from plan:\n%s", FormatPlan(plan))
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
		"ARIVU_BROWSER_CAPTURE_ENABLED=true",
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
	if opts.Version != "v1.2.3" || opts.ProxyMode != ProxyExistingProxy || opts.TLSEmail != "ops@example.net" || opts.BackupEnabled || !opts.CaptureEnabled {
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
