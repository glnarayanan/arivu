package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/glnarayanan/arivu/internal/buildinfo"
	"github.com/glnarayanan/arivu/internal/installer"
)

func main() {
	log.SetFlags(0)
	if buildinfo.WriteIfRequested(os.Stdout, "arivu-installer", os.Args[1:]) {
		return
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	switch os.Args[1] {
	case "install":
		runInstall(ctx, os.Args[2:])
	case "plan":
		runPlan(ctx, os.Args[2:])
	case "status":
		runStatus(ctx, os.Args[2:])
	case "backup":
		runBackup(os.Args[2:])
	case "restore":
		runRestore(os.Args[2:])
	case "upgrade":
		runUpgrade(ctx, os.Args[2:])
	case "reconfigure":
		runInstall(ctx, append([]string{"--reconfigure"}, os.Args[2:]...))
	case "uninstall":
		runUninstall(ctx, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: arivu-installer <command>

commands:
  version      print the installed installer version
  install      interactive end-to-end server install
  plan         print the detected install plan without changing the host
  status       print host and Arivu install status
  backup       create a consistent SQLite backup under /var/backups/arivu
  restore      restore a backup directory
  upgrade      verify and install the latest Arivu binary, then restart service
  reconfigure  rerun the install wizard against an existing install
  uninstall    stop and remove service files; use --purge to remove data`)
}

func runInstall(ctx context.Context, args []string) {
	opts, applyOpts, nonInteractive, yes, flagsSet, err := parseOptions(args)
	if err != nil {
		log.Fatal(err)
	}
	if opts.Reconfigure {
		opts = mergeExistingOptions(opts, flagsSet)
	}
	applyOpts.InstallBinary = !opts.Reconfigure || flagsSet["version"] || flagsSet["artifact-url"] || flagsSet["checksums-url"]
	if !nonInteractive {
		opts, applyOpts, err = interactiveWizard(opts, applyOpts)
		if err != nil {
			log.Fatal(err)
		}
	}
	if err := validateInstallOptions(opts, applyOpts, nonInteractive, true); err != nil {
		log.Fatal(err)
	}
	facts := detectHostForOptions(ctx, opts)
	plan, err := installer.BuildPlan(opts, facts)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(installer.FormatPlan(plan))
	if applyOpts.DryRun {
		fmt.Println("Dry run complete; no changes applied.")
		return
	}
	if !nonInteractive && !yes && !confirm("Apply this plan?") {
		log.Fatal("install cancelled")
	}
	if err := installer.Apply(ctx, plan, applyOpts); err != nil {
		log.Fatal(err)
	}
	fmt.Print(completionMessage(plan))
}

func runPlan(ctx context.Context, args []string) {
	opts, applyOpts, nonInteractive, _, flagsSet, err := parseOptions(args)
	if err != nil {
		log.Fatal(err)
	}
	if opts.Reconfigure {
		opts = mergeExistingOptions(opts, flagsSet)
	}
	if !nonInteractive && opts.Domain == "" {
		opts, _, err = interactiveWizard(opts, installer.ApplyOptions{DryRun: true})
		if err != nil {
			log.Fatal(err)
		}
	}
	if err := validateInstallOptions(opts, applyOpts, nonInteractive, false); err != nil {
		log.Fatal(err)
	}
	facts := detectHostForOptions(ctx, opts)
	plan, err := installer.BuildPlan(opts, facts)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(installer.FormatPlan(plan))
}

func parseOptions(args []string) (installer.Options, installer.ApplyOptions, bool, bool, map[string]bool, error) {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	var opts installer.Options
	var apply installer.ApplyOptions
	var proxyMode string
	var nonInteractive, yes bool
	fs.StringVar(&opts.Domain, "domain", "", "Domain or subdomain for Arivu")
	fs.StringVar(&opts.AdminEmail, "admin-email", "", "First admin email")
	fs.StringVar(&opts.TLSEmail, "tls-email", "", "Email for TLS certificate notices")
	fs.StringVar(&proxyMode, "proxy-mode", string(installer.ProxyAuto), "auto, managed-caddy, existing-proxy, existing, or app-only")
	fs.StringVar(&opts.Version, "version", "", "Release version to install; empty/latest uses the latest release")
	fs.BoolVar(&opts.SignupsEnabled, "signups-enabled", false, "Allow public signups after install")
	fs.BoolVar(&opts.BackupEnabled, "backups", true, "Install daily SQLite backup timer")
	fs.BoolVar(&opts.SkipDNSCheck, "skip-dns-check", false, "Skip domain-to-server DNS verification")
	fs.IntVar(&opts.BindPort, "bind-port", 0, "Loopback port for Arivu")
	fs.StringVar(&apply.AdminPasswordFile, "admin-password-file", "", "Path containing first admin password")
	fs.StringVar(&apply.ArtifactURL, "artifact-url", "", "Arivu binary artifact URL")
	fs.StringVar(&apply.ChecksumsURL, "checksums-url", "", "SHA256SUMS URL")
	fs.BoolVar(&apply.DryRun, "dry-run", false, "Validate and render without changing the host")
	fs.BoolVar(&nonInteractive, "non-interactive", false, "Do not prompt; require flags")
	fs.BoolVar(&yes, "yes", false, "Apply without the final interactive confirmation")
	fs.BoolVar(&opts.Reconfigure, "reconfigure", false, "Reconfigure an existing install")
	if err := fs.Parse(args); err != nil {
		return opts, apply, false, false, nil, err
	}
	flagsSet := map[string]bool{}
	fs.Visit(func(flag *flag.Flag) {
		flagsSet[flag.Name] = true
	})
	opts.ProxyMode = installer.NormalizeProxyMode(proxyMode)
	return opts, apply, nonInteractive, yes, flagsSet, nil
}

func validateInstallOptions(opts installer.Options, apply installer.ApplyOptions, nonInteractive bool, requireInstallPassword bool) error {
	if nonInteractive {
		if strings.TrimSpace(opts.Domain) == "" || strings.TrimSpace(opts.AdminEmail) == "" {
			return fmt.Errorf("--domain and --admin-email are required with --non-interactive")
		}
		if requireInstallPassword && !opts.Reconfigure && apply.AdminPasswordFile == "" && !apply.DryRun {
			return fmt.Errorf("--admin-password-file is required with --non-interactive install")
		}
	}
	return nil
}

func detectHostForOptions(ctx context.Context, opts installer.Options) installer.HostFacts {
	domain := opts.Domain
	if opts.SkipDNSCheck {
		domain = ""
	}
	return installer.DetectHost(ctx, domain)
}

func completionMessage(plan installer.Plan) string {
	if installer.RequiresManualFirewall(plan) {
		var b strings.Builder
		fmt.Fprintf(&b, "Arivu service and Caddy installed for %s, but public HTTPS still needs firewall access.\n", plan.Options.Domain)
		b.WriteString("Run these additive firewall commands when ready:\n")
		for _, command := range installer.FirewallCommands(plan.Facts.Firewall) {
			fmt.Fprintf(&b, "- %s\n", command)
		}
		return b.String()
	}
	if plan.ProxyMode == installer.ProxyManagedCaddy {
		return fmt.Sprintf("Arivu install complete: https://%s\n", plan.Options.Domain)
	}
	if plan.ProxyMode == installer.ProxyAppOnly {
		return fmt.Sprintf("Arivu service installed on %s:%d; configure your reverse proxy manually using the snippets printed above for %s.\n", plan.BindAddress, plan.BindPort, plan.Options.Domain)
	}
	return fmt.Sprintf("Arivu service installed on %s:%d; finish proxy integration for %s.\n", plan.BindAddress, plan.BindPort, plan.Options.Domain)
}

func mergeExistingOptions(opts installer.Options, flagsSet map[string]bool) installer.Options {
	existing, err := installer.OptionsFromEnvFile("/etc/arivu/arivu.env")
	if err != nil {
		return opts
	}
	if !flagsSet["domain"] {
		opts.Domain = defaultString(opts.Domain, existing.Domain)
	}
	if !flagsSet["admin-email"] {
		opts.AdminEmail = defaultString(opts.AdminEmail, existing.AdminEmail)
	}
	if !flagsSet["bind-port"] && opts.BindPort == 0 {
		opts.BindPort = existing.BindPort
	}
	if !flagsSet["signups-enabled"] {
		opts.SignupsEnabled = existing.SignupsEnabled
	}
	if !flagsSet["proxy-mode"] && existing.ProxyMode != "" {
		opts.ProxyMode = existing.ProxyMode
	}
	if !flagsSet["version"] {
		opts.Version = defaultString(opts.Version, existing.Version)
	}
	if !flagsSet["tls-email"] {
		opts.TLSEmail = defaultString(opts.TLSEmail, existing.TLSEmail)
	}
	if !flagsSet["backups"] {
		opts.BackupEnabled = existing.BackupEnabled
	}
	return opts
}

func interactiveWizard(opts installer.Options, apply installer.ApplyOptions) (installer.Options, installer.ApplyOptions, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return opts, apply, fmt.Errorf("interactive install requires a terminal; rerun with --non-interactive flags")
	}
	defer tty.Close()
	return interactiveWizardWithIO(bufio.NewReader(tty), tty, opts, apply)
}

func interactiveWizardWithReader(reader *bufio.Reader, opts installer.Options, apply installer.ApplyOptions) (installer.Options, installer.ApplyOptions, error) {
	return interactiveWizardWithIO(reader, os.Stdout, opts, apply)
}

func interactiveWizardWithIO(reader *bufio.Reader, out io.Writer, opts installer.Options, apply installer.ApplyOptions) (installer.Options, installer.ApplyOptions, error) {
	opts.Domain = promptWithWriter(reader, out, "Domain/subdomain", opts.Domain)
	opts.AdminEmail = promptWithWriter(reader, out, "Admin email", opts.AdminEmail)
	opts.TLSEmail = promptWithWriter(reader, out, "TLS notification email", defaultString(opts.TLSEmail, opts.AdminEmail))
	mode := promptWithWriter(reader, out, "Proxy mode [auto, managed-caddy, existing-proxy, app-only]", defaultString(string(opts.ProxyMode), string(installer.ProxyAuto)))
	opts.ProxyMode = installer.NormalizeProxyMode(mode)
	opts.SignupsEnabled = promptBoolWithWriter(reader, out, "Allow public signups", opts.SignupsEnabled)
	opts.BackupEnabled = promptBoolWithWriter(reader, out, "Install daily SQLite backups", opts.BackupEnabled)
	if !apply.DryRun && !opts.Reconfigure && apply.AdminPasswordFile == "" && apply.AdminPassword == "" {
		password, err := readSecret("First admin password")
		if err != nil {
			return opts, apply, err
		}
		apply.AdminPassword = password
	}
	return opts, apply, nil
}

func prompt(reader *bufio.Reader, label string, fallback string) string {
	return promptWithWriter(reader, os.Stdout, label, fallback)
}

func promptWithWriter(reader *bufio.Reader, out io.Writer, label string, fallback string) string {
	if fallback != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, fallback)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	value, _ := reader.ReadString('\n')
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func promptBool(reader *bufio.Reader, label string, fallback bool) bool {
	return promptBoolWithWriter(reader, os.Stdout, label, fallback)
}

func promptBoolWithWriter(reader *bufio.Reader, out io.Writer, label string, fallback bool) bool {
	defaultLabel := "n"
	if fallback {
		defaultLabel = "y"
	}
	value := strings.ToLower(promptWithWriter(reader, out, label+" [y/n]", defaultLabel))
	return value == "y" || value == "yes" || value == "true" || value == "1"
}

var openTTY = func() (io.ReadWriteCloser, error) {
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}

func confirm(label string) bool {
	tty, err := openTTY()
	if err != nil {
		reader := bufio.NewReader(os.Stdin)
		return promptBool(reader, label, false)
	}
	defer tty.Close()
	return promptBoolWithWriter(bufio.NewReader(tty), tty, label, false)
}

func readSecret(label string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		reader := bufio.NewReader(os.Stdin)
		return prompt(reader, label, ""), nil
	}
	defer tty.Close()
	fmt.Fprintf(tty, "%s: ", label)
	_ = exec.Command("stty", "-F", "/dev/tty", "-echo").Run()
	defer func() {
		_ = exec.Command("stty", "-F", "/dev/tty", "echo").Run()
		fmt.Fprintln(tty)
	}()
	reader := bufio.NewReader(tty)
	value, err := reader.ReadString('\n')
	return strings.TrimRight(value, "\r\n"), err
}

func runStatus(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	domain := fs.String("domain", "", "Domain to include in DNS checks")
	_ = fs.Parse(args)
	facts := installer.DetectHost(ctx, *domain)
	fmt.Printf("OS: %s %s\nArch: %s\nSystemd: %v\nFirewall: %s\nArivu user: %v\n/etc/arivu: %v\n/var/lib/arivu: %v\nService: %v\n", facts.OSID, facts.OSVersionID, facts.Arch, facts.HasSystemd, facts.Firewall, facts.UserExists, facts.EtcExists, facts.DataExists, facts.ServiceExists)
	if len(facts.Listeners) > 0 {
		fmt.Println("Listeners:")
		for port, detail := range facts.Listeners {
			fmt.Printf("- %d: %s\n", port, detail)
		}
	}
}

func runBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	root := fs.String("root", "/", "Install root")
	quiet := fs.Bool("quiet", false, "Only print errors")
	_ = fs.Parse(args)
	path, err := installer.Backup(*root)
	if err != nil {
		log.Fatal(err)
	}
	if !*quiet {
		fmt.Println(path)
	}
}

func runRestore(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	root := fs.String("root", "/", "Install root")
	backup := fs.String("backup", "", "Backup directory to restore")
	_ = fs.Parse(args)
	if err := installer.Restore(*root, *backup); err != nil {
		log.Fatal(err)
	}
}

func runUpgrade(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	artifactURL := fs.String("artifact-url", "", "Arivu binary artifact URL")
	installerArtifactURL := fs.String("installer-artifact-url", "", "Arivu installer binary artifact URL")
	checksumsURL := fs.String("checksums-url", "", "SHA256SUMS URL")
	version := fs.String("version", "", "Release version to install; empty/latest uses the latest release")
	_ = fs.Parse(args)
	facts := installer.DetectHost(ctx, "")
	if err := installer.Upgrade(ctx, facts, installer.ApplyOptions{ArtifactURL: *artifactURL, InstallerArtifactURL: *installerArtifactURL, ChecksumsURL: *checksumsURL}, *version); err != nil {
		log.Fatal(err)
	}
}

func runUninstall(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "Remove /etc/arivu, /var/lib/arivu, and backups")
	_ = fs.Parse(args)
	if err := installer.Uninstall(ctx, *purge); err != nil {
		log.Fatal(err)
	}
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
