package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"
)

// Pool adalah satu entri proxy pool di 9router-go.
// PERHATIAN: API daftar pools mengembalikan field DATAR (name, proxyUrl, ...)
// di level atas — bukan di dalam objek "data". Struct ini pernah salah bentuk
// (mengharapkan data.proxyUrl) sehingga pencocokan selalu kosong dan daemon
// membuat 30 pool duplikat di SETIAP siklus. Jangan diubah tanpa mengecek
// respons asli GET /api/proxy-pools.
type Pool struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProxyURL   string `json:"proxyUrl"`
	IsActive   bool   `json:"isActive"`
	TestStatus string `json:"testStatus"`
	Data       struct {
		Name     string `json:"name"`
		ProxyURL string `json:"proxyUrl"`
	} `json:"data"`
}

// URL mengembalikan proxyUrl pool dari field datar, dengan fallback ke
// bentuk bersarang bila suatu saat API berubah lagi.
func (p Pool) URL() string {
	if p.ProxyURL != "" {
		return p.ProxyURL
	}
	return p.Data.ProxyURL
}

// Router adalah klien API admin 9router-go (login sesi cookie).
type Router struct {
	base   string
	client *http.Client
}

func routerPassword(cfg Config) (string, error) {
	if cfg.RouterPass != "" {
		return cfg.RouterPass, nil
	}
	b, err := os.ReadFile(cfg.RouterEnvFile)
	if err != nil {
		return "", fmt.Errorf("baca %s: %w", cfg.RouterEnvFile, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "INITIAL_PASSWORD=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "INITIAL_PASSWORD=")), nil
		}
	}
	return "", fmt.Errorf("INITIAL_PASSWORD tidak ditemukan di %s", cfg.RouterEnvFile)
}

func NewRouter(cfg Config) (*Router, error) {
	pw, err := routerPassword(cfg)
	if err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	r := &Router{base: cfg.RouterURL, client: &http.Client{Jar: jar, Timeout: 60 * time.Second}}
	body, _ := json.Marshal(map[string]string{"password": pw})
	resp, err := r.client.Post(r.base+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("login router: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("login router: HTTP %d", resp.StatusCode)
	}
	return r, nil
}

func (r *Router) do(method, path string, payload any) ([]byte, int, error) {
	var rd io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, r.base+path, rd)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return b, resp.StatusCode, err
}

func (r *Router) ListPools() ([]Pool, error) {
	b, code, err := r.do("GET", "/api/proxy-pools?includeUsage=true", nil)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("daftar pools: HTTP %d", code)
	}
	var wrap struct {
		ProxyPools []Pool `json:"proxyPools"`
	}
	if err := json.Unmarshal(b, &wrap); err != nil {
		return nil, err
	}
	return wrap.ProxyPools, nil
}

func (r *Router) CreatePool(name, url string) error {
	_, code, err := r.do("POST", "/api/proxy-pools", map[string]any{
		"name": name, "proxyUrl": url, "noProxy": "", "isActive": true,
	})
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("buat pool %s: HTTP %d", name, code)
	}
	return nil
}

// SyncPools memastikan setiap instance punya proxy pool di router
// (dicocokkan lewat proxyUrl, yang tidak pernah berubah walau akun dirotasi).
// Mengembalikan jumlah pool yang baru dibuat.
func (r *Router) SyncPools(cfg Config) (int, error) {
	pools, err := r.ListPools()
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	counts := map[string]int{}
	for _, p := range pools {
		have[p.URL()] = true
		counts[p.URL()]++
	}
	// Pengaman anti-runaway: bila daftar pools sudah mengandung duplikat
	// atau jauh lebih besar dari armada, JANGAN membuat apa pun — keadaan
	// seperti itu berarti pencocokan sedang tidak bisa dipercaya.
	for u, c := range counts {
		if c > 1 {
			return 0, fmt.Errorf("sinkron dibatalkan: %d pools duplikat untuk %s — bereskan dulu", c, u)
		}
	}
	if len(pools) > cfg.Count*2 {
		return 0, fmt.Errorf("sinkron dibatalkan: jumlah pools (%d) jauh melebihi armada (%d)", len(pools), cfg.Count)
	}
	created := 0
	for n := 1; n <= cfg.Count; n++ {
		if !have[cfg.PoolURL(n)] {
			if err := r.CreatePool(cfg.PoolName(n), cfg.PoolURL(n)); err != nil {
				return created, err
			}
			created++
		}
	}
	return created, nil
}
