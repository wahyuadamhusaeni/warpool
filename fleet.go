package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func runCmd(ctx context.Context, timeout time.Duration, dir, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func docker(ctx context.Context, args ...string) (string, error) {
	return runCmd(ctx, 90*time.Second, "", "docker", args...)
}

func containerExists(ctx context.Context, name string) bool {
	_, err := docker(ctx, "inspect", "-f", "{{.State.Running}}", name)
	return err == nil
}

func containerRunning(ctx context.Context, name string) bool {
	out, err := docker(ctx, "inspect", "-f", "{{.State.Running}}", name)
	return err == nil && out == "true"
}

// ensureContainer membuat container instance bila belum jalan, lengkap dengan
// port host dan sambungan ke jaringan router.
func ensureContainer(ctx context.Context, cfg Config, n int) error {
	name := cfg.Name(n)
	if containerRunning(ctx, name) {
		return nil
	}
	_, _ = docker(ctx, "rm", "-f", name)
	out, err := docker(ctx, "run", "-d",
		"--name", name,
		"--restart", "unless-stopped",
		"-p", fmt.Sprintf("%d:1080", cfg.HostPort(n)),
		"-v", cfg.AcctDir(n)+":/etc/usque:ro",
		cfg.Image)
	if err != nil {
		return fmt.Errorf("docker run %s: %v (%s)", name, err, out)
	}
	// Sambungkan ke jaringan router; abaikan error "sudah tersambung".
	_, _ = docker(ctx, "network", "connect", cfg.Network, name)
	return nil
}

// --- registrasi akun usque ---

var regMu sync.Mutex
var lastReg time.Time

func hasAccount(cfg Config, n int) bool {
	_, err := os.Stat(filepath.Join(cfg.AcctDir(n), "config.json"))
	return err == nil
}

// registerAccount mendaftarkan akun WARP baru untuk instance n.
// Bila force=false dan config sudah ada, tidak melakukan apa-apa.
// Registrasi diberi jarak minimum karena Cloudflare membatasi laju
// pendaftaran device dari satu IP (pengamatan: ~10 per ledakan, lalu menetes).
func registerAccount(ctx context.Context, cfg Config, n int, force bool) error {
	dir := cfg.AcctDir(n)
	cfgPath := filepath.Join(dir, "config.json")
	if !force && hasAccount(cfg, n) {
		return nil
	}
	regMu.Lock()
	defer regMu.Unlock()
	if wait := time.Until(lastReg.Add(cfg.RegGap)); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if force {
		_ = os.Remove(cfgPath)
	}
	out, err := runCmd(ctx, 90*time.Second, dir, cfg.UsqueBin, "register", "--accept-tos")
	lastReg = time.Now()
	if err != nil {
		return fmt.Errorf("usque register acct%d: %v (%s)", n, err, out)
	}
	if !hasAccount(cfg, n) {
		return fmt.Errorf("usque register acct%d: config.json tidak terbentuk (%s)", n, out)
	}
	_ = os.Chmod(cfgPath, 0o600)
	return nil
}

// registerWithBackoff mencoba registrasi dengan jeda yang membesar,
// mengantisipasi rate limit Cloudflare.
func registerWithBackoff(ctx context.Context, cfg Config, n int, force bool) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(30*(1<<(attempt-1))) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err = registerAccount(ctx, cfg, n, force); err == nil {
			return nil
		}
	}
	return err
}
