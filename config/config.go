package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Addr              string   // ADDR, listen address
	DataDir           string   // DATA_DIR
	PublicURL         string   // PUBLIC_URL, base for object URLs and presigned links
	AccessKey         string   // ACCESS_KEY
	SecretKey         string   // SECRET_KEY
	Region            string   // REGION
	VerifyAuth        bool     // AUTH=verify checks signatures; AUTH=off (default) accepts any
	Buckets           []string // BUCKETS, created at startup
	AutoCreateBuckets bool     // AUTO_CREATE_BUCKETS
	CORSOrigins       []string // CORS_ORIGINS
	LogFile           string   // LOG_FILE; "off" disables file logging
}

// Load reads .env (if present) and the environment.
func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("reading .env: %v", err)
	}
	return FromEnv(os.Getenv)
}

// FromEnv builds a Config from getenv, applying defaults.
func FromEnv(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	list := func(key, def string) []string {
		var out []string
		for v := range strings.SplitSeq(get(key, def), ",") {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		return out
	}

	c := Config{
		Addr:        get("ADDR", ":8080"),
		DataDir:     get("DATA_DIR", "./data"),
		AccessKey:   get("ACCESS_KEY", "local"),
		SecretKey:   get("SECRET_KEY", "localsecret"),
		Region:      get("REGION", "us-east-1"),
		Buckets:     list("BUCKETS", "laravel"),
		CORSOrigins: list("CORS_ORIGINS", "*"),
		LogFile:     get("LOG_FILE", "app.log"),
	}
	if c.LogFile == "off" {
		c.LogFile = ""
	}

	switch mode := get("AUTH", "off"); mode {
	case "off":
	case "verify":
		c.VerifyAuth = true
	default:
		return c, fmt.Errorf("AUTH must be off or verify, got %q", mode)
	}

	var err error
	if c.AutoCreateBuckets, err = strconv.ParseBool(get("AUTO_CREATE_BUCKETS", "false")); err != nil {
		return c, fmt.Errorf("AUTO_CREATE_BUCKETS: %w", err)
	}

	port := c.Addr[strings.LastIndex(c.Addr, ":")+1:]
	c.PublicURL = strings.TrimRight(get("PUBLIC_URL", "http://localhost:"+port), "/")
	return c, nil
}
