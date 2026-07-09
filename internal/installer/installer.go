package installer

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type ProxyMode string

const (
	ProxyAuto          ProxyMode = "auto"
	ProxyManagedCaddy  ProxyMode = "managed-caddy"
	ProxyExistingProxy ProxyMode = "existing-proxy"
	ProxyAppOnly       ProxyMode = "app-only"
)

type Options struct {
	Domain         string
	AdminEmail     string
	TLSEmail       string
	ProxyMode      ProxyMode
	SignupsEnabled bool
	BackupEnabled  bool
	SkipDNSCheck   bool
	BindPort       int
	Version        string
	Reconfigure    bool
}

type HostFacts struct {
	OSID           string
	OSVersionID    string
	Arch           string
	HasSystemd     bool
	UserExists     bool
	EtcExists      bool
	DataExists     bool
	ServiceExists  bool
	Commands       map[string]string
	Listeners      map[int]string
	ExistingVHosts []string
	PublicIP       string
	DomainIPs      []string
	Firewall       string
}

type Plan struct {
	Options     Options
	Facts       HostFacts
	ProxyMode   ProxyMode
	BindAddress string
	BindPort    int
	Actions     []Action
	Files       []ManagedFile
	Warnings    []string
}

type Action struct {
	Name   string
	Detail string
}

type ManagedFile struct {
	Path    string
	Mode    string
	Content string
}

func BuildPlan(opts Options, facts HostFacts) (Plan, error) {
	var err error
	opts.Domain, err = normalizeDomain(opts.Domain)
	if err != nil {
		return Plan{}, err
	}
	opts.Version = strings.TrimSpace(opts.Version)
	opts.AdminEmail, err = normalizeEmail(opts.AdminEmail)
	if err != nil {
		return Plan{}, fmt.Errorf("admin email is invalid: %w", err)
	}
	if strings.TrimSpace(opts.TLSEmail) != "" {
		opts.TLSEmail, err = normalizeEmail(opts.TLSEmail)
		if err != nil {
			return Plan{}, fmt.Errorf("TLS email is invalid: %w", err)
		}
	}
	if opts.ProxyMode == "" {
		opts.ProxyMode = ProxyAuto
	}
	if err := validateOptions(opts); err != nil {
		return Plan{}, err
	}
	if facts.Commands == nil {
		facts.Commands = map[string]string{}
	}
	if facts.Listeners == nil {
		facts.Listeners = map[int]string{}
	}
	if err := validateHostFacts(opts, facts); err != nil {
		return Plan{}, err
	}
	mode, warnings, err := chooseProxyMode(opts, facts)
	if err != nil {
		return Plan{}, err
	}
	port := opts.BindPort
	if port == 0 {
		port = firstFreePort(facts.Listeners, 8090)
	}
	plan := Plan{
		Options:     opts,
		Facts:       facts,
		ProxyMode:   mode,
		BindAddress: "127.0.0.1",
		BindPort:    port,
		Warnings:    warnings,
	}
	plan.Actions = plannedActions(opts, facts, mode, port)
	plan.Files = plannedFiles(opts, facts, mode, port)
	return plan, nil
}

func validateOptions(opts Options) error {
	if opts.Domain == "" {
		return errors.New("domain is required")
	}
	switch opts.ProxyMode {
	case ProxyAuto, ProxyManagedCaddy, ProxyExistingProxy, ProxyAppOnly:
	default:
		return fmt.Errorf("unsupported proxy mode %q", opts.ProxyMode)
	}
	if opts.BindPort != 0 && (opts.BindPort < 1024 || opts.BindPort > 65535) {
		return errors.New("bind port must be between 1024 and 65535")
	}
	return nil
}

func validateHostFacts(opts Options, facts HostFacts) error {
	if facts.OSID != "" {
		switch facts.OSID {
		case "ubuntu":
		case "debian":
		default:
			return fmt.Errorf("unsupported Linux distribution %s", facts.OSID)
		}
	}
	if facts.Arch != "" && facts.Arch != "amd64" && facts.Arch != "arm64" {
		return fmt.Errorf("unsupported architecture %s", facts.Arch)
	}
	if !facts.HasSystemd {
		return errors.New("systemd is required")
	}
	for _, host := range facts.ExistingVHosts {
		if sameHost(host, opts.Domain) {
			if opts.Reconfigure && (facts.EtcExists || facts.DataExists || facts.ServiceExists) {
				continue
			}
			return fmt.Errorf("domain %s already appears in an existing proxy vhost", opts.Domain)
		}
	}
	if !opts.SkipDNSCheck && facts.PublicIP != "" && len(facts.DomainIPs) > 0 && !containsIP(facts.DomainIPs, facts.PublicIP) {
		return fmt.Errorf("domain %s does not resolve to this server IP %s", opts.Domain, facts.PublicIP)
	}
	return nil
}

func chooseProxyMode(opts Options, facts HostFacts) (ProxyMode, []string, error) {
	if opts.ProxyMode != ProxyAuto {
		return opts.ProxyMode, explicitModeWarnings(opts.ProxyMode, facts), nil
	}
	hasProxy := facts.Commands["caddy"] != "" || facts.Commands["nginx"] != "" || facts.Commands["apache2"] != "" || facts.Commands["httpd"] != ""
	if facts.Listeners[80] != "" || facts.Listeners[443] != "" || hasProxy {
		return ProxyExistingProxy, []string{"Detected an existing web stack or occupied 80/443; Arivu will use an existing-proxy plan and bind only to loopback."}, nil
	}
	return ProxyManagedCaddy, nil, nil
}

func explicitModeWarnings(mode ProxyMode, facts HostFacts) []string {
	if mode == ProxyManagedCaddy && (facts.Listeners[80] != "" || facts.Listeners[443] != "") {
		return []string{"managed-caddy was requested but 80/443 are occupied; installer will require confirmation before changing Caddy."}
	}
	if mode == ProxyAppOnly {
		return []string{"app-only mode will not configure TLS or reverse proxy files."}
	}
	return nil
}

func firstFreePort(listeners map[int]string, start int) int {
	for port := start; port < 65535; port++ {
		if listeners[port] == "" {
			return port
		}
	}
	return start
}

func plannedActions(opts Options, facts HostFacts, mode ProxyMode, port int) []Action {
	actions := []Action{
		{"Install packages", "Install ca-certificates, curl, sqlite3, and service/proxy packages after confirmation."},
		{"Prepare users and directories", "Create arivu system user plus /etc/arivu, /var/lib/arivu, and /var/backups/arivu without touching unrelated apps."},
		{"Install Arivu binary", "Download the release artifact, verify SHA256SUMS, and install /usr/local/bin/arivu."},
		{"Bootstrap first admin", "Run arivu admin bootstrap with the password provided through stdin."},
		{"Install systemd service", fmt.Sprintf("Run Arivu on 127.0.0.1:%d with a hardened arivu.service.", port)},
	}
	switch mode {
	case ProxyManagedCaddy:
		actions = append(actions, Action{"Configure Caddy", "Install an Arivu-owned Caddy site block; do not replace global Caddy config."})
	case ProxyExistingProxy:
		detail := "Write an Arivu proxy snippet for the detected proxy and ask before validating/reloading it."
		if facts.Commands["nginx"] != "" {
			detail = "Write /etc/nginx/snippets/arivu.conf and ask before nginx -t and reload."
		} else if facts.Commands["caddy"] != "" {
			detail = "Write /etc/arivu/proxy/Caddyfile.arivu and ask before caddy validate/reload."
		} else if facts.Commands["apache2"] != "" || facts.Commands["httpd"] != "" {
			detail = "Write /etc/apache2/conf-available/arivu.conf and ask before Apache validation/reload."
		}
		actions = append(actions, Action{"Integrate existing proxy", detail})
	case ProxyAppOnly:
		actions = append(actions, Action{"Print proxy snippets", "Leave web server configuration untouched and print Caddy/Nginx examples."})
	}
	if opts.BackupEnabled {
		actions = append(actions, Action{"Install backup timer", "Back up SQLite with a consistent online snapshot under /var/backups/arivu."})
	}
	if facts.Firewall != "" {
		actions = append(actions, Action{"Propose firewall additions", "Only add HTTP/HTTPS allowances when needed; never reset existing firewall rules."})
	}
	return actions
}

func plannedFiles(opts Options, facts HostFacts, mode ProxyMode, port int) []ManagedFile {
	envOpts := opts
	envOpts.ProxyMode = mode
	files := []ManagedFile{
		{Path: "/etc/arivu/arivu.env", Mode: "0640", Content: EnvFile(envOpts, port, "GENERATED-BY-INSTALLER")},
		{Path: "/etc/systemd/system/arivu.service", Mode: "0644", Content: ServiceFile()},
	}
	if opts.BackupEnabled {
		files = append(files,
			ManagedFile{Path: "/etc/systemd/system/arivu-backup.service", Mode: "0644", Content: BackupServiceFile()},
			ManagedFile{Path: "/etc/systemd/system/arivu-backup.timer", Mode: "0644", Content: BackupTimerFile()},
		)
	}
	switch mode {
	case ProxyManagedCaddy:
		files = append(files, ManagedFile{Path: "/etc/caddy/conf.d/arivu.caddy", Mode: "0644", Content: CaddyFile(opts.Domain, port, opts.TLSEmail)})
	case ProxyExistingProxy:
		files = append(files, existingProxyFiles(opts, facts, port)...)
	}
	return files
}

func existingProxyFiles(opts Options, facts HostFacts, port int) []ManagedFile {
	switch {
	case facts.Commands["nginx"] != "":
		return []ManagedFile{{Path: "/etc/nginx/snippets/arivu.conf", Mode: "0644", Content: NginxSnippet(opts.Domain, port)}}
	case facts.Commands["caddy"] != "":
		return []ManagedFile{{Path: "/etc/arivu/proxy/Caddyfile.arivu", Mode: "0644", Content: CaddyFile(opts.Domain, port, opts.TLSEmail)}}
	case facts.Commands["apache2"] != "" || facts.Commands["httpd"] != "":
		return []ManagedFile{{Path: "/etc/apache2/conf-available/arivu.conf", Mode: "0644", Content: ApacheSnippet(opts.Domain, port)}}
	default:
		return []ManagedFile{
			{Path: "/etc/arivu/proxy/Caddyfile.arivu", Mode: "0644", Content: CaddyFile(opts.Domain, port, opts.TLSEmail)},
			{Path: "/etc/arivu/proxy/nginx.conf", Mode: "0644", Content: NginxSnippet(opts.Domain, port)},
			{Path: "/etc/arivu/proxy/apache.conf", Mode: "0644", Content: ApacheSnippet(opts.Domain, port)},
		}
	}
}

func EnvFile(opts Options, port int, secret string) string {
	signups := "false"
	if opts.SignupsEnabled {
		signups = "true"
	}
	backups := "false"
	if opts.BackupEnabled {
		backups = "true"
	}
	lines := []string{
		fmt.Sprintf("ARIVU_ADDR=127.0.0.1:%d", port),
		"ARIVU_DB=/var/lib/arivu/arivu.sqlite3",
		"APP_URL=https://" + opts.Domain,
		"COOKIE_SECURE=true",
		"SIGNUPS_ENABLED=" + signups,
		"ADMIN_EMAILS=" + opts.AdminEmail,
		"SECRET_KEY=" + secret,
		"ARIVU_FETCH_USER_AGENT=Arivu/2.0",
		"ARIVU_INSTALLER_VERSION=" + opts.Version,
		"ARIVU_INSTALLER_PROXY_MODE=" + string(opts.ProxyMode),
		"ARIVU_TLS_EMAIL=" + opts.TLSEmail,
		"ARIVU_BACKUPS_ENABLED=" + backups,
		"",
	}
	return strings.Join(lines, "\n")
}

func ServiceFile() string {
	return `[Unit]
Description=Arivu self-hosted knowledge service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=arivu
Group=arivu
WorkingDirectory=/var/lib/arivu
EnvironmentFile=/etc/arivu/arivu.env
ExecStart=/usr/local/bin/arivu serve
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/arivu /var/backups/arivu
CapabilityBoundingSet=
LockPersonality=true
MemoryDenyWriteExecute=true
RestrictRealtime=true
RestrictSUIDSGID=true
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
`
}

func BackupServiceFile() string {
	return `[Unit]
Description=Back up Arivu SQLite data

[Service]
Type=oneshot
ExecStart=/usr/local/bin/arivu-installer backup --quiet
`
}

func BackupTimerFile() string {
	return `[Unit]
Description=Daily Arivu SQLite backup

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
`
}

func CaddyFile(domain string, port int, tlsEmail string) string {
	tlsLine := ""
	if tlsEmail != "" {
		tlsLine = "\ttls " + tlsEmail + "\n"
	}
	return fmt.Sprintf(`%s {
%s	reverse_proxy 127.0.0.1:%d
}
`, domain, tlsLine, port)
}

func NginxSnippet(domain string, port int) string {
	return fmt.Sprintf(`server {
	listen 80;
	server_name %s;

	location / {
		proxy_set_header Host $host;
		proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
		proxy_set_header X-Forwarded-Proto $scheme;
		proxy_pass http://127.0.0.1:%d;
	}
}
`, domain, port)
}

func ApacheSnippet(domain string, port int) string {
	return fmt.Sprintf(`<VirtualHost *:80>
	ServerName %s
	ProxyPreserveHost On
	ProxyPass / http://127.0.0.1:%d/
	ProxyPassReverse / http://127.0.0.1:%d/
</VirtualHost>
`, domain, port, port)
}

func containsIP(values []string, needle string) bool {
	parsedNeedle := net.ParseIP(needle)
	for _, value := range values {
		if parsed := net.ParseIP(value); parsed != nil && parsedNeedle != nil && parsed.Equal(parsedNeedle) {
			return true
		}
	}
	return false
}

func GenerateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func ReleaseArtifactURLs(repo string, version string, arch string) (string, string) {
	base := strings.TrimRight(repo, "/") + "/releases/latest/download"
	if version != "" && version != "latest" {
		base = strings.TrimRight(repo, "/") + "/releases/download/" + version
	}
	return base + "/arivu-linux-" + arch, base + "/SHA256SUMS"
}

func FormatPlan(plan Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Arivu install plan for %s\n", plan.Options.Domain)
	if plan.Options.Version != "" && plan.Options.Version != "latest" {
		fmt.Fprintf(&b, "Release: %s\n", plan.Options.Version)
	} else {
		b.WriteString("Release: latest\n")
	}
	fmt.Fprintf(&b, "Proxy mode: %s\n", plan.ProxyMode)
	fmt.Fprintf(&b, "App bind: %s:%d\n\n", plan.BindAddress, plan.BindPort)
	if len(plan.Warnings) > 0 {
		b.WriteString("Warnings:\n")
		for _, warning := range plan.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
		b.WriteString("\n")
	}
	b.WriteString("Actions:\n")
	for _, action := range plan.Actions {
		fmt.Fprintf(&b, "- %s: %s\n", action.Name, action.Detail)
	}
	if len(plan.Files) > 0 {
		b.WriteString("\nManaged files:\n")
		sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].Path < plan.Files[j].Path })
		for _, file := range plan.Files {
			fmt.Fprintf(&b, "- %s (%s)\n", file.Path, file.Mode)
		}
	}
	if RequiresManualFirewall(plan) {
		b.WriteString("\nManual firewall commands required before public HTTPS is complete:\n")
		for _, command := range FirewallCommands(plan.Facts.Firewall) {
			fmt.Fprintf(&b, "- %s\n", command)
		}
	}
	if plan.ProxyMode == ProxyAppOnly {
		b.WriteString("\nManual proxy snippets:\n")
		b.WriteString(AppOnlyProxySnippets(plan))
	}
	return b.String()
}

// RequiresManualFirewall reports whether public HTTP/HTTPS still depends on operator-owned firewall changes.
func RequiresManualFirewall(plan Plan) bool {
	return plan.ProxyMode == ProxyManagedCaddy && strings.TrimSpace(plan.Facts.Firewall) != ""
}

// FirewallCommands returns additive commands for opening HTTP/HTTPS on the detected firewall.
func FirewallCommands(firewall string) []string {
	switch strings.TrimSpace(firewall) {
	case "ufw":
		return []string{"sudo ufw allow 80/tcp", "sudo ufw allow 443/tcp"}
	case "firewalld":
		return []string{"sudo firewall-cmd --permanent --add-service=http", "sudo firewall-cmd --permanent --add-service=https", "sudo firewall-cmd --reload"}
	default:
		return []string{"open TCP ports 80 and 443 in the host firewall"}
	}
}

// AppOnlyProxySnippets renders copyable reverse-proxy examples without adding managed proxy files.
func AppOnlyProxySnippets(plan Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nCaddy:\n%s", CaddyFile(plan.Options.Domain, plan.BindPort, plan.Options.TLSEmail))
	fmt.Fprintf(&b, "\nNginx:\n%s", NginxSnippet(plan.Options.Domain, plan.BindPort))
	fmt.Fprintf(&b, "\nApache:\n%s", ApacheSnippet(plan.Options.Domain, plan.BindPort))
	return b.String()
}

func ParsePort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	if port < 1024 || port > 65535 {
		return 0, errors.New("port must be between 1024 and 65535")
	}
	return port, nil
}

func OptionsFromEnvFile(path string) (Options, error) {
	file, err := os.Open(path)
	if err != nil {
		return Options{}, err
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	if err := scanner.Err(); err != nil {
		return Options{}, err
	}
	return optionsFromEnv(values), nil
}

func optionsFromEnv(values map[string]string) Options {
	opts := Options{}
	if appURL := strings.TrimSpace(values["APP_URL"]); appURL != "" {
		opts.Domain = domainFromAppURL(appURL)
	}
	if adminEmails := strings.TrimSpace(values["ADMIN_EMAILS"]); adminEmails != "" {
		opts.AdminEmail = strings.TrimSpace(strings.Split(adminEmails, ",")[0])
	}
	if addr := strings.TrimSpace(values["ARIVU_ADDR"]); addr != "" {
		opts.BindPort = portFromAddr(addr)
	}
	opts.Version = strings.TrimSpace(values["ARIVU_INSTALLER_VERSION"])
	opts.ProxyMode = NormalizeProxyMode(values["ARIVU_INSTALLER_PROXY_MODE"])
	opts.TLSEmail = strings.TrimSpace(values["ARIVU_TLS_EMAIL"])
	opts.SignupsEnabled = truthy(values["SIGNUPS_ENABLED"])
	if _, ok := values["ARIVU_BACKUPS_ENABLED"]; ok {
		opts.BackupEnabled = truthy(values["ARIVU_BACKUPS_ENABLED"])
	} else {
		opts.BackupEnabled = true
	}
	return opts
}

func domainFromAppURL(value string) string {
	parsed, err := url.Parse(value)
	if err == nil && parsed.Host != "" {
		return parsed.Hostname()
	}
	return strings.TrimPrefix(strings.TrimPrefix(value, "https://"), "http://")
}

func portFromAddr(addr string) int {
	if _, portValue, err := net.SplitHostPort(addr); err == nil {
		port, _ := ParsePort(portValue)
		return port
	}
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		port, _ := ParsePort(addr[idx+1:])
		return port
	}
	return 0
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func normalizeEmail(value string) (string, error) {
	parsed, err := mail.ParseAddress(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return strings.ToLower(parsed.Address), nil
}

func normalizeDomain(value string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(value))
	if host == "" {
		return "", nil
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, "/:;{}") {
		return "", errors.New("domain must be a hostname, not a URL or proxy directive")
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("domain must not contain whitespace or control characters")
		}
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) == 0 || len(host) > 253 {
		return "", errors.New("domain must be a valid DNS hostname")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return "", errors.New("domain must be a valid DNS hostname")
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", errors.New("domain labels must not start or end with hyphen")
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return "", errors.New("domain must contain only DNS hostname characters")
			}
		}
	}
	return host, nil
}

func sameHost(candidate string, domain string) bool {
	candidate = strings.TrimSpace(strings.ToLower(candidate))
	if strings.Contains(candidate, "://") {
		if parsed, err := url.Parse(candidate); err == nil {
			candidate = parsed.Hostname()
		}
	}
	candidate = strings.TrimSuffix(strings.Trim(candidate, "[]"), ".")
	if host, _, err := net.SplitHostPort(candidate); err == nil {
		candidate = host
	}
	return candidate == strings.TrimSuffix(strings.ToLower(domain), ".")
}

func NormalizeProxyMode(value string) ProxyMode {
	value = strings.TrimSpace(value)
	if value == "existing" {
		return ProxyExistingProxy
	}
	return ProxyMode(value)
}
