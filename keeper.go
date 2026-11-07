package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"
)

// InstanceStatus adalah keadaan satu instance pada satu siklus pemeriksaan.
type InstanceStatus struct {
	N    int    `json:"n"`
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
	Pool bool   `json:"pool"`
	Note string `json:"note,omitempty"`
}

func probeAll(cfg Config) []InstanceStatus {
	out := make([]InstanceStatus, cfg.Count)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for n := 1; n <= cfg.Count; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := probeInstance(cfg, n)
			st := InstanceStatus{N: n, Name: cfg.Name(n), OK: r.OK, IPv4: r.IPv4, IPv6: r.IPv6}
			if !r.OK {
				st.Note = r.Err
			}
			out[n-1] = st
		}(n)
	}
	wg.Wait()
	return out
}

func writeStatus(cfg Config, sts []InstanceStatus) {
	doc := map[string]any{
		"checkedAt": time.Now().Format(time.RFC3339),
		"instances": sts,
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	tmp := cfg.StatusPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err == nil {
		_ = os.Rename(tmp, cfg.StatusPath())
	}
}

// runCycle menjalankan satu siklus penuh: probe semua instance,
// sinkronkan proxy pools di router, lalu sembuhkan yang bermasalah.
func runCycle(ctx context.Context, cfg Config) []InstanceStatus {
	sts := probeAll(cfg)

	if cfg.RouterURL != "" {
		if r, err := NewRouter(cfg); err != nil {
			log.Printf("router: %v (sinkron pools dilewati)", err)
		} else {
			pools, err := r.ListPools()
			if err != nil {
				log.Printf("router: daftar pools gagal: %v", err)
			} else {
				have := map[string]bool{}
				for _, p := range pools {
					have[p.Data.ProxyURL] = true
				}
				for i := range sts {
					sts[i].Pool = have[cfg.PoolURL(sts[i].N)]
				}
				created, err := r.SyncPools(cfg)
				if err != nil {
					log.Printf("router: sinkron pools gagal: %v", err)
				} else if created > 0 {
					log.Printf("router: %d proxy pool baru dibuat", created)
					for i := range sts {
						sts[i].Pool = true
					}
				}
			}
		}
	}

	heal(ctx, cfg, sts)
	writeStatus(cfg, sts)
	return sts
}

// heal menyembuhkan instance yang mati dan yang egress IPv4-nya dobel.
// Registrasi ulang dibatasi per siklus (anggaran) karena rate limit
// Cloudflare; sisanya dilanjutkan siklus berikutnya — armada konvergen
// ke unik secara bertahap, bukan sekali ledakan.
func heal(ctx context.Context, cfg Config, sts []InstanceStatus) {
	budget := cfg.HealMaxReg

	// 1) Instance mati: pastikan container jalan; bila tetap mati, registrasi ulang.
	for i := range sts {
		st := &sts[i]
		if st.OK {
			continue
		}
		if !hasAccount(cfg, st.N) {
			// Belum punya akun sama sekali (mis. sisa pembangunan armada):
			// daftarkan bila anggaran siklus ini masih ada.
			if budget <= 0 {
				st.Note = "belum ada akun; menunggu siklus berikut"
				continue
			}
			if err := registerAccount(ctx, cfg, st.N, false); err != nil {
				st.Note = "registrasi gagal: " + err.Error()
				continue
			}
			budget--
			_ = ensureContainer(ctx, cfg, st.N)
			if r := probeInstance(cfg, st.N); r.OK {
				st.OK, st.IPv4, st.IPv6, st.Note = true, r.IPv4, r.IPv6, "akun dibuat & pulih"
				log.Printf("%s: akun baru dibuat, egress %s", st.Name, r.IPv4)
			}
			continue
		}
		_ = ensureContainer(ctx, cfg, st.N)
		if r := probeInstance(cfg, st.N); r.OK {
			st.OK, st.IPv4, st.IPv6, st.Note = true, r.IPv4, r.IPv6, "pulih; container dipastikan jalan"
			log.Printf("%s: pulih, egress %s", st.Name, r.IPv4)
			continue
		}
		if budget <= 0 {
			st.Note = "mati; registrasi ulang menunggu siklus berikut (anggaran habis)"
			continue
		}
		if err := registerWithBackoff(ctx, cfg, st.N, true); err != nil {
			st.Note = "registrasi ulang gagal: " + err.Error()
			continue
		}
		budget--
		_, _ = docker(ctx, "rm", "-f", st.Name)
		_ = ensureContainer(ctx, cfg, st.N)
		if r := probeInstance(cfg, st.N); r.OK {
			st.OK, st.IPv4, st.IPv6, st.Note = true, r.IPv4, r.IPv6, "pulih setelah registrasi ulang"
			log.Printf("%s: registrasi ulang berhasil, egress %s", st.Name, r.IPv4)
		} else {
			st.Note = "masih mati setelah registrasi ulang"
		}
	}

	// 2) Egress dobel: instance bernomor terkecil dipertahankan,
	//    sisanya diundi ulang.
	byIP := map[string][]int{}
	for _, st := range sts {
		if st.OK && st.IPv4 != "" {
			byIP[st.IPv4] = append(byIP[st.IPv4], st.N)
		}
	}
	for ip, ns := range byIP {
		if len(ns) < 2 {
			continue
		}
		sort.Ints(ns)
		for _, n := range ns[1:] {
			st := &sts[n-1]
			if budget <= 0 {
				st.Note = fmt.Sprintf("egress dobel %s dengan warp-%d; undi ulang menunggu siklus berikut", ip, ns[0])
				continue
			}
			if err := registerWithBackoff(ctx, cfg, n, true); err != nil {
				st.Note = "undi ulang gagal: " + err.Error()
				continue
			}
			budget--
			_, _ = docker(ctx, "rm", "-f", cfg.Name(n))
			_ = ensureContainer(ctx, cfg, n)
			if r := probeInstance(cfg, n); r.OK {
				st.IPv4, st.IPv6 = r.IPv4, r.IPv6
				if r.IPv4 == ip {
					st.Note = fmt.Sprintf("undi ulang masih dapat %s (dicoba lagi siklus berikut)", ip)
				} else {
					st.Note = fmt.Sprintf("egress diganti %s -> %s", ip, r.IPv4)
					log.Printf("%s: egress diganti %s -> %s", cfg.Name(n), ip, r.IPv4)
				}
			}
		}
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func printTable(sts []InstanceStatus) int {
	bad := 0
	for _, st := range sts {
		mark := "OK  "
		if !st.OK {
			mark = "MATI"
			bad++
		}
		pool := "pool:-"
		if st.Pool {
			pool = "pool:ada"
		}
		fmt.Printf("%-8s %s ipv4=%-16s ipv6=%-30s %s %s\n",
			st.Name, mark, dash(st.IPv4), dash(st.IPv6), pool, st.Note)
	}
	return bad
}

func cmdCheck(cfg Config) int {
	sts := runCycle(context.Background(), cfg)
	return printTable(sts)
}

func cmdProvision(cfg Config) error {
	ctx := context.Background()
	for n := 1; n <= cfg.Count; n++ {
		if !hasAccount(cfg, n) {
			if err := registerAccount(ctx, cfg, n, false); err != nil {
				log.Printf("%s: registrasi ditunda (%v) — keeper akan melanjutkan", cfg.Name(n), err)
			}
		}
		if err := ensureContainer(ctx, cfg, n); err != nil {
			log.Printf("%s: %v", cfg.Name(n), err)
		}
	}
	sts := runCycle(ctx, cfg)
	printTable(sts)
	return nil
}

func cmdRotate(cfg Config, n int) error {
	ctx := context.Background()
	before := probeInstance(cfg, n)
	if err := registerWithBackoff(ctx, cfg, n, true); err != nil {
		return err
	}
	_, _ = docker(ctx, "rm", "-f", cfg.Name(n))
	if err := ensureContainer(ctx, cfg, n); err != nil {
		return err
	}
	after := probeInstance(cfg, n)
	fmt.Printf("%s: egress %s -> %s (ipv6 %s)\n",
		cfg.Name(n), dash(before.IPv4), dash(after.IPv4), dash(after.IPv6))
	return nil
}

func cmdDaemon(cfg Config) {
	log.Printf("warpool daemon mulai: %d instance, interval %s, anggaran registrasi %d/siklus",
		cfg.Count, cfg.Interval, cfg.HealMaxReg)
	for {
		sts := runCycle(context.Background(), cfg)
		ok := 0
		for _, st := range sts {
			if st.OK {
				ok++
			}
		}
		log.Printf("siklus selesai: %d/%d instance sehat", ok, len(sts))
		time.Sleep(cfg.Interval)
	}
}
