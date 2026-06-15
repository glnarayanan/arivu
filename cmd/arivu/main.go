package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/glnarayanan/arivu/internal/app"
	"github.com/glnarayanan/arivu/internal/config"
	"github.com/glnarayanan/arivu/internal/migrate"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "serve":
			runServe(os.Args[2:])
			return
		case "migrate":
			runMigrate(os.Args[2:])
			return
		case "version":
			fmt.Println("arivu")
			return
		case "login":
			runLogin(os.Args[2:])
			return
		case "save":
			runSave(os.Args[2:])
			return
		case "list":
			runList(os.Args[2:])
			return
		case "search":
			runSearch(os.Args[2:])
			return
		}
	}

	runServe(os.Args[1:])
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", envDefault("ARIVU_ADDR", ":8080"), "HTTP listen address")
	dbPath := fs.String("db", envDefault("ARIVU_DB", "arivu.sqlite3"), "SQLite database path")
	_ = fs.Parse(args)

	cfg := config.FromEnv()
	cfg.Addr = *addr
	cfg.DBPath = *dbPath

	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("initialize app: %v", err)
	}
	defer application.Close()

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           application.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	go func() {
		log.Printf("arivu listening on %s", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func runMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	source := fs.String("mongo-uri", "", "MongoDB URI for legacy migration discovery")
	var exportPath string
	fs.StringVar(&exportPath, "mongo-export", "", "Path to a dependency-free JSON export file or directory to validate")
	fs.StringVar(&exportPath, "json-export", "", "Alias for --mongo-export")
	dbName := fs.String("mongo-db", "arivu_db", "MongoDB database name")
	out := fs.String("out", "migration-manifest.json", "Manifest output path")
	dryRun := fs.Bool("dry-run", true, "Only discover and validate legacy schema")
	sampleLimit := fs.Int("sample-limit", 1000, "Maximum documents to validate per collection from JSON exports")
	sqliteDB := fs.String("sqlite-db", envDefault("ARIVU_DB", "arivu.sqlite3"), "SQLite database path for --dry-run=false")
	oldSecretKey := fs.String("old-secret-key", envDefault("OLD_SECRET_KEY", ""), "Legacy SECRET_KEY for decrypting exported runtime secrets")
	newSecretKey := fs.String("new-secret-key", envDefault("SECRET_KEY", ""), "New SECRET_KEY for re-encrypting migrated runtime secrets")
	keyID := fs.String("key-id", "primary", "Key identifier to store with migrated encrypted settings")
	allowExisting := fs.Bool("allow-existing", false, "Allow applying into a non-empty SQLite database")
	_ = fs.Parse(args)

	if *source == "" && exportPath == "" {
		log.Fatal("--mongo-uri, --mongo-export, or --json-export is required for migration discovery")
	}
	if !*dryRun {
		report, err := migrate.ApplyExport(context.Background(), migrate.ApplyOptions{
			ExportPath:    exportPath,
			DBPath:        *sqliteDB,
			OldSecretKey:  *oldSecretKey,
			NewSecretKey:  *newSecretKey,
			KeyID:         *keyID,
			SampleLimit:   *sampleLimit,
			DryRun:        false,
			AllowExisting: *allowExisting,
		})
		if err != nil {
			log.Fatalf("migration apply: %v", err)
		}
		raw, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(raw))
		return
	}
	if err := migrate.DiscoverMongoSchema(context.Background(), migrate.Options{
		MongoURI:    *source,
		ExportPath:  exportPath,
		DBName:      *dbName,
		OutPath:     *out,
		DryRun:      *dryRun,
		SampleLimit: *sampleLimit,
	}); err != nil {
		log.Fatalf("migration discovery: %v", err)
	}
}

func runLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	apiURL := fs.String("api", envDefault("ARIVU_API", "http://127.0.0.1:8080/api"), "Arivu API base URL")
	email := fs.String("email", "", "Email")
	password := fs.String("password", "", "Password")
	_ = fs.Parse(args)
	if *email == "" || *password == "" {
		log.Fatal("--email and --password are required")
	}
	var out map[string]any
	if err := cliRequest(http.MethodPost, *apiURL+"/auth/cli/login", map[string]string{"email": *email, "password": *password}, "", &out); err != nil {
		log.Fatal(err)
	}
	access, _ := out["access_token"].(string)
	refresh, _ := out["refresh_token"].(string)
	if access == "" {
		log.Fatal("login response did not include access_token")
	}
	if err := saveCLIConfig(*apiURL, access, refresh); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Logged in")
}

func runSave(args []string) {
	fs := flag.NewFlagSet("save", flag.ExitOnError)
	apiURL := fs.String("api", "", "Arivu API base URL")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		log.Fatal("usage: arivu save <url>")
	}
	cfg := loadCLIConfig()
	if *apiURL != "" {
		cfg.APIURL = *apiURL
	}
	var out map[string]any
	if err := cliRequest(http.MethodPost, cfg.APIURL+"/cli/bookmarks", map[string]string{"url": fs.Arg(0)}, cfg.AccessToken, &out); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Saved", fs.Arg(0))
}

func runList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	apiURL := fs.String("api", "", "Arivu API base URL")
	_ = fs.Parse(args)
	cfg := loadCLIConfig()
	if *apiURL != "" {
		cfg.APIURL = *apiURL
	}
	var out []map[string]any
	if err := cliRequest(http.MethodGet, cfg.APIURL+"/cli/bookmarks", nil, cfg.AccessToken, &out); err != nil {
		log.Fatal(err)
	}
	printBookmarks(out)
}

func runSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	apiURL := fs.String("api", "", "Arivu API base URL")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		log.Fatal("usage: arivu search <query>")
	}
	cfg := loadCLIConfig()
	if *apiURL != "" {
		cfg.APIURL = *apiURL
	}
	var out []map[string]any
	if err := cliRequest(http.MethodGet, cfg.APIURL+"/cli/bookmarks?search="+url.QueryEscape(fs.Arg(0)), nil, cfg.AccessToken, &out); err != nil {
		log.Fatal(err)
	}
	printBookmarks(out)
}

type cliConfig struct {
	APIURL       string `json:"api_url"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func cliRequest(method string, target string, body any, token string, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API status %d: %s", resp.StatusCode, string(raw))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func printBookmarks(bookmarks []map[string]any) {
	for _, bm := range bookmarks {
		fmt.Printf("%s\t%s\t%s\n", bm["id"], bm["title"], bm["url"])
	}
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ".arivu.json"
	}
	return filepath.Join(dir, "arivu", "config.json")
}

func saveCLIConfig(apiURL, access, refresh string) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cliConfig{APIURL: apiURL, AccessToken: access, RefreshToken: refresh}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func loadCLIConfig() cliConfig {
	raw, err := os.ReadFile(configPath())
	if err != nil {
		log.Fatal("not logged in; run arivu login first")
	}
	var cfg cliConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		log.Fatal(err)
	}
	return cfg
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
