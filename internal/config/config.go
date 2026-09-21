// Package config loads runtime configuration from a .env file and the process
// environment. Environment variables always win over the .env file.
package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr                string
	BaseURL             string
	DatabasePath        string
	StorageDir          string
	DemoMode            bool
	VerticalPack        string
	AIProviderConfig    string // path to ai.providers.json (optional)
	AIProvider          string // "demo" or a provider id to prefer
	EnableEmbeddings    bool
	EmbeddingsBaseURL   string
	EmbeddingsModel     string
	EmbeddingsAPIKey    string
	SessionSecret       string
	MaxUploadBytes      int64
	Workers             int
	CronSecret          string
	EnablePublicSandbox bool
	SandboxTTLHours     int
	SlackWebhookURL     string
	ClickUpToken        string
	ClickUpListID       string
	ErrorWebhookURL     string
	InboxDir            string
	EnableInbox         bool
	DebugPrompts        bool
	LogJSON             bool
}

// Load reads ".env" from the working directory (if present) and the environment.
func Load() Config {
	loadDotEnv(".env")
	c := Config{
		Addr:                get("BIDOS_ADDR", ":"+get("PORT", "8080")),
		BaseURL:             get("BASE_URL", "http://localhost:8080"),
		DatabasePath:        get("DATABASE_URL", "storage/bidos.db"),
		StorageDir:          get("STORAGE_DIR", "storage"),
		DemoMode:            getBool("DEMO_MODE", true),
		VerticalPack:        get("VERTICAL_PACK", "saas-it"),
		AIProviderConfig:    get("AI_ROUTER_CONFIG", "ai.providers.json"),
		AIProvider:          get("AI_PROVIDER", "auto"),
		EnableEmbeddings:    getBool("ENABLE_EMBEDDINGS", true),
		EmbeddingsBaseURL:   get("EMBEDDINGS_BASE_URL", ""),
		EmbeddingsModel:     get("EMBEDDINGS_MODEL", ""),
		EmbeddingsAPIKey:    get("EMBEDDINGS_API_KEY", ""),
		SessionSecret:       get("SESSION_SECRET", ""),
		MaxUploadBytes:      getInt64("MAX_UPLOAD_BYTES", 25<<20),
		Workers:             int(getInt64("WORKERS", 4)),
		CronSecret:          get("CRON_SECRET", ""),
		EnablePublicSandbox: getBool("ENABLE_PUBLIC_SANDBOX", false),
		SandboxTTLHours:     int(getInt64("SANDBOX_TTL_HOURS", 24)),
		SlackWebhookURL:     get("SLACK_WEBHOOK_URL", ""),
		ClickUpToken:        get("CLICKUP_TOKEN", ""),
		ClickUpListID:       get("CLICKUP_LIST_ID", ""),
		ErrorWebhookURL:     get("ERROR_WEBHOOK_URL", ""),
		InboxDir:            get("INBOX_DIR", "storage/inbox"),
		EnableInbox:         getBool("ENABLE_EMAIL_INTAKE", false),
		DebugPrompts:        getBool("DEBUG_PROMPTS", false),
		LogJSON:             getBool("LOG_JSON", false),
	}
	return c
}

func get(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func getBool(k string, def bool) bool {
	v := strings.ToLower(get(k, ""))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func getInt64(k string, def int64) int64 {
	if v, err := strconv.ParseInt(get(k, ""), 10, 64); err == nil {
		return v
	}
	return def
}

// loadDotEnv sets variables from a KEY=VALUE file without overriding existing env.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}
