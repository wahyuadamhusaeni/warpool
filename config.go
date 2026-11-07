package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config dibaca dari environment variable; lihat README untuk daftar lengkapnya.
type Config struct {
	Count         int           // WARPOOL_COUNT — jumlah instance (warp-1..warp-N)
	BaseDir       string        // WARPOOL_BASE_DIR — direktori kerja armada di host
	Image         string        // WARPOOL_IMAGE — image container instance (usque)
	Network       string        // WARPOOL_NETWORK — jaringan docker yang juga dipakai router
	BasePort      int           // WARPOOL_BASE_PORT — port host instance pertama
	ProbeHost     string        // WARPOOL_PROBE_HOST — kosong = probe via nama container
	UsqueBin      string        // USQUE_BIN — lokasi binary usque
	RouterURL     string        // ROUTER_URL — base URL 9router-go; kosong = sinkron pools mati
	RouterPass    string        // ROUTER_PASSWORD — password admin router
	RouterEnvFile string        // ROUTER_ENV_FILE — fallback: baca INITIAL_PASSWORD dari file env router
	Interval      time.Duration // CHECK_INTERVAL — jeda antar siklus daemon
	RegGap        time.Duration // REG_MIN_GAP — jarak minimum antar registrasi akun
	HealMaxReg    int           // HEAL_MAX_REGISTER — maks registrasi ulang per siklus keeper
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func loadConfig() (Config, error) {
	c := Config{
		Count:         envInt("WARPOOL_COUNT", 30),
		BaseDir:       envOr("WARPOOL_BASE_DIR", "/srv/apps/warpgo"),
		Image:         envOr("WARPOOL_IMAGE", "warpool-usque:latest"),
		Network:       envOr("WARPOOL_NETWORK", "public-net"),
		BasePort:      envInt("WARPOOL_BASE_PORT", 1080),
		ProbeHost:     os.Getenv("WARPOOL_PROBE_HOST"),
		UsqueBin:      envOr("USQUE_BIN", "/usr/local/bin/usque"),
		RouterURL:     strings.TrimRight(os.Getenv("ROUTER_URL"), "/"),
		RouterPass:    os.Getenv("ROUTER_PASSWORD"),
		RouterEnvFile: envOr("ROUTER_ENV_FILE", "/srv/apps/9router-go/.env"),
		Interval:      envDur("CHECK_INTERVAL", 15*time.Minute),
		RegGap:        envDur("REG_MIN_GAP", 20*time.Second),
		HealMaxReg:    envInt("HEAL_MAX_REGISTER", 3),
	}
	if c.Count < 1 || c.Count > 200 {
		return c, fmt.Errorf("WARPOOL_COUNT di luar rentang: %d", c.Count)
	}
	return c, nil
}

func (c Config) Name(n int) string  { return fmt.Sprintf("warp-%d", n) }
func (c Config) HostPort(n int) int { return c.BasePort + n - 1 }

func (c Config) AcctDir(n int) string {
	return filepath.Join(c.BaseDir, "usque", fmt.Sprintf("acct%d", n))
}

// ProbeAddr adalah alamat SOCKS5 instance yang dipakai prober.
func (c Config) ProbeAddr(n int) string {
	if c.ProbeHost != "" {
		return fmt.Sprintf("%s:%d", c.ProbeHost, c.HostPort(n))
	}
	return fmt.Sprintf("%s:1080", c.Name(n))
}

func (c Config) PoolName(n int) string { return fmt.Sprintf("WARP %d", n) }
func (c Config) PoolURL(n int) string  { return fmt.Sprintf("socks5://%s:1080", c.Name(n)) }
func (c Config) StatusPath() string    { return filepath.Join(c.BaseDir, "warpool-status.json") }
