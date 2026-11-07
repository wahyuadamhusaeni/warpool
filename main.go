package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
)

func main() {
	log.SetFlags(log.LstdFlags)
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("konfigurasi: %v", err)
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "provision":
		if err := cmdProvision(cfg); err != nil {
			log.Fatalf("provision: %v", err)
		}
	case "check":
		os.Exit(cmdCheck(cfg))
	case "daemon":
		cmdDaemon(cfg)
	case "rotate":
		if len(os.Args) < 3 {
			log.Fatalf("pakai: warpool rotate <nomor-instance>")
		}
		n, err := strconv.Atoi(os.Args[2])
		if err != nil || n < 1 || n > cfg.Count {
			log.Fatalf("nomor instance tidak valid: %s", os.Args[2])
		}
		if err := cmdRotate(cfg, n); err != nil {
			log.Fatalf("rotate: %v", err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `warpool — pengelola armada proxy WARP (usque) untuk proxy pools 9router

Perintah:
  provision   bangun/perbaiki armada (akun + container), lalu sinkronkan pools
  check       satu kali pemeriksaan semua instance (kode keluar = jumlah bermasalah)
  daemon      keeper berkala: probe, sinkron pools, sembuhkan yang dobel/mati
  rotate <n>  registrasi ulang instance n sampai egress-nya berganti`)
}
