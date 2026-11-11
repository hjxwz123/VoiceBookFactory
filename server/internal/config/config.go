// Package config loads runtime configuration from environment variables (and an optional .env file).
package config

import (
	"bufio"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Addr      string
	PublicURL string
	DataDir   string
	WebDir    string

	DBDriver string // sqlite | postgres
	DBDSN    string

	// RedisURL (redis://[:password@]host:6379/0) shares realtime events, job wake-ups, rate limits,
	// cache invalidation and scheduled-task locks between instances. Empty keeps all of it in-process,
	// which is right for a single instance.
	RedisURL string

	JWTSecret []byte
	SecretKey []byte // 32 bytes, AES-256-GCM for provider credentials
	// DefaultSecrets is true when VB_JWT_SECRET or VB_SECRET_KEY was left unset: fine for development,
	// refused in production (anyone could forge sessions or read the stored API keys).
	DefaultSecrets bool

	AdminEmail    string
	AdminPassword string

	DevMode bool
	Workers int

	FFmpeg  string
	FFprobe string

	// Optional bootstrap for the first LLM / ASR provider account (can be managed in the admin console later).
	LLMProtocol string
	LLMBaseURL  string
	LLMAPIKey   string
	LLMModel    string
	ASRBaseURL  string
	ASRAPIKey   string
	ASRModel    string
	// BotContact is the operator contact (email or URL) sent in the User-Agent when the server downloads
	// library files from Wikimedia Commons and OpenGameArt, as their robot policies require.
	BotContact string
}

func Load() *Config {
	loadDotEnv(".env")
	c := &Config{
		Addr:          env("VB_ADDR", ":8080"),
		PublicURL:     strings.TrimRight(env("VB_PUBLIC_URL", "http://localhost:8080"), "/"),
		DataDir:       env("VB_DATA_DIR", "./data"),
		WebDir:        env("VB_WEB_DIR", "../web/dist"),
		DBDriver:      env("VB_DB_DRIVER", "sqlite"),
		DBDSN:         env("VB_DB_DSN", ""),
		RedisURL:      strings.TrimSpace(env("VB_REDIS_URL", "")),
		AdminEmail:    env("VB_ADMIN_EMAIL", "admin@example.com"),
		AdminPassword: env("VB_ADMIN_PASSWORD", "Admin@123456"),
		DevMode:       envBool("VB_DEV", true),
		Workers:       envInt("VB_WORKERS", 4),
		FFmpeg:        env("VB_FFMPEG", "ffmpeg"),
		FFprobe:       env("VB_FFPROBE", "ffprobe"),
		LLMProtocol:   env("VB_LLM_PROTOCOL", "openai_chat"),
		LLMBaseURL:    env("VB_LLM_BASE_URL", ""),
		LLMAPIKey:     env("VB_LLM_API_KEY", ""),
		LLMModel:      env("VB_LLM_MODEL", ""),
		ASRBaseURL:    env("VB_ASR_BASE_URL", ""),
		ASRAPIKey:     env("VB_ASR_API_KEY", ""),
		ASRModel:      env("VB_ASR_MODEL", "whisper-1"),
		BotContact:    strings.TrimSpace(env("VB_BOT_CONTACT", "")),
	}
	if c.DBDSN == "" && c.DBDriver == "sqlite" {
		c.DBDSN = filepath.Join(c.DataDir, "voicebook.db")
	}
	jwt, key := env("VB_JWT_SECRET", ""), env("VB_SECRET_KEY", "")
	c.DefaultSecrets = jwt == "" || key == "" || jwt == "change-me" || key == "change-me"
	c.JWTSecret = derive(firstSet(jwt, "dev-jwt-secret-change-me"))
	c.SecretKey = derive(firstSet(key, "dev-secret-key-change-me"))
	return c
}

func firstSet(v, def string) string {
	if v == "" || v == "change-me" {
		return def
	}
	return v
}

func derive(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v := strings.ToLower(os.Getenv(k))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// loadDotEnv reads KEY=VALUE lines without overriding variables already set.
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
