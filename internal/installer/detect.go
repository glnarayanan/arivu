package installer

import (
	"bufio"
	"context"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func DetectHost(ctx context.Context, domain string) HostFacts {
	facts := HostFacts{
		Arch:      runtime.GOARCH,
		Commands:  map[string]string{},
		Listeners: map[int]string{},
		Firewall:  "",
	}
	facts.OSID, facts.OSVersionID = readOSRelease()
	for _, name := range []string{"apt-get", "curl", "sqlite3", "caddy", "nginx", "apache2", "apache2ctl", "httpd", "docker", "ufw", "firewall-cmd", "systemctl", "ss"} {
		if path, err := exec.LookPath(name); err == nil {
			facts.Commands[name] = path
		}
	}
	facts.HasSystemd = facts.Commands["systemctl"] != ""
	if facts.Commands["ufw"] != "" {
		facts.Firewall = "ufw"
	} else if facts.Commands["firewall-cmd"] != "" {
		facts.Firewall = "firewalld"
	}
	facts.Listeners = detectListeners(ctx, facts.Commands["ss"])
	facts.UserExists = userExists("arivu")
	facts.EtcExists = pathExists("/etc/arivu")
	facts.DataExists = pathExists("/var/lib/arivu")
	facts.ServiceExists = pathExists("/etc/systemd/system/arivu.service")
	facts.ExistingVHosts = detectVHosts()
	if domain != "" {
		facts.PublicIP = publicIP(ctx)
		facts.DomainIPs = lookupDomain(ctx, domain)
	}
	return facts
}

func readOSRelease() (string, string) {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return "", ""
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok {
			values[key] = strings.Trim(value, `"`)
		}
	}
	return values["ID"], values["VERSION_ID"]
}

func detectListeners(ctx context.Context, ssPath string) map[int]string {
	result := map[int]string{}
	if ssPath == "" {
		for _, port := range []int{80, 443, 8080, 8090} {
			conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 150*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				result[port] = "tcp listener"
			}
		}
		return result
	}
	out, err := exec.CommandContext(ctx, ssPath, "-ltnp").Output()
	if err != nil {
		return result
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		for _, field := range fields {
			if idx := strings.LastIndex(field, ":"); idx >= 0 {
				if port, err := strconv.Atoi(strings.Trim(field[idx+1:], "*")); err == nil {
					result[port] = line
				}
			}
		}
	}
	return result
}

func userExists(name string) bool {
	file, err := os.Open("/etc/passwd")
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	prefix := name + ":"
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), prefix) {
			return true
		}
	}
	return false
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func detectVHosts() []string {
	roots := []string{"/etc/caddy", "/etc/nginx", "/etc/apache2", "/etc/httpd"}
	hosts := map[string]bool{}
	for _, root := range roots {
		_ = walkConfig(root, func(line string) {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "server_name ") {
				for _, host := range strings.Fields(strings.TrimSuffix(strings.TrimPrefix(line, "server_name "), ";")) {
					hosts[host] = true
				}
			}
			if strings.HasPrefix(line, "ServerName ") {
				fields := strings.Fields(line)
				if len(fields) > 1 {
					hosts[fields[1]] = true
				}
			}
			for _, host := range caddyHostsFromLine(line) {
				hosts[host] = true
			}
		})
	}
	result := make([]string, 0, len(hosts))
	for host := range hosts {
		result = append(result, host)
	}
	return result
}

func walkConfig(root string, fn func(string)) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".conf") && !strings.HasSuffix(path, "Caddyfile") && !strings.HasSuffix(path, ".caddy") {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fn(scanner.Text())
		}
		return nil
	})
}

func caddyHostsFromLine(line string) []string {
	line = strings.TrimSpace(strings.Split(line, "#")[0])
	if !strings.Contains(line, "{") {
		return nil
	}
	prefix := strings.TrimSpace(strings.SplitN(line, "{", 2)[0])
	if prefix == "" {
		return nil
	}
	switch strings.Fields(prefix)[0] {
	case "handle", "handle_path", "route", "reverse_proxy", "respond", "redir", "tls", "log", "encode", "header", "file_server":
		return nil
	}
	var hosts []string
	for _, part := range strings.FieldsFunc(prefix, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*:") {
			continue
		}
		if strings.Contains(part, "://") {
			if parsed, err := url.Parse(part); err == nil {
				part = parsed.Hostname()
			}
		}
		if strings.Contains(part, "/") {
			continue
		}
		if host, _, err := net.SplitHostPort(part); err == nil {
			part = host
		}
		if part != "" && !strings.ContainsAny(part, "{}") {
			hosts = append(hosts, part)
		}
	}
	return hosts
}

func publicIP(ctx context.Context) string {
	for _, endpoint := range []string{"https://api.ipify.org", "https://ifconfig.me/ip"} {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		out, err := exec.CommandContext(ctx, "curl", "-fsSL", endpoint).Output()
		cancel()
		if err == nil {
			if ip := strings.TrimSpace(string(out)); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	return ""
}

func lookupDomain(ctx context.Context, domain string) []string {
	resolver := net.Resolver{}
	ips, err := resolver.LookupIPAddr(ctx, domain)
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(ips))
	for _, ip := range ips {
		result = append(result, ip.IP.String())
	}
	return result
}
