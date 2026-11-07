# warpool

Pengelola armada proxy **Cloudflare WARP** super-ringan untuk dipakai sebagai
**proxy pools di [9router-go](https://github.com/wahyuadamhusaeni)** (dan router
serupa). Satu instance = satu container [usque](https://github.com/Diniboy1123/usque)
(MASQUE) ≈ **3 MiB RAM**, dengan egress yang dijaga **unik per instance** oleh
keeper yang menyembuhkan diri sendiri.

```
                 ┌──────────────────────────────────────────────┐
                 │ warpool (daemon, Go, <15 MiB)                │
                 │  probe egress ──► deteksi dobel/mati         │
                 │  sinkron pools ─► 9router-go /api/proxy-pools│
                 │  sembuhkan ─────► registrasi ulang akun      │
                 └───────┬──────────────────────────────────────┘
                         │ docker socket
      ┌──────────────────┼──────────────────┐
      ▼                  ▼                  ▼
 warp-1 (usque)    warp-2 (usque)  ...  warp-30 (usque)     ~3 MiB each
 socks5 :1080      socks5 :1080         socks5 :1080
 egress .124       egress .128          egress unik...
      └──────────────────┬──────────────────┘
                         ▼
              9router-go proxy pools "WARP N"
              socks5://warp-N:1080 (tidak pernah berubah)
```

## Kenapa MASQUE lewat TCP, bukan WireGuard?

Di jaringan VPS ini **UDP keluar tersaring**: handshake WireGuard (semua port)
dan QUIC tidak pernah selesai — sudah diuji tuntas. Yang tembus hanya
**MASQUE lewat TCP 443**, jadi usque selalu dijalankan dengan mode `--http2`.
Ini juga alasan proyek relay WARP berbasis WireGuard tidak bisa dipakai di sini.

Fakta lapangan lain yang membentuk desain:

- Egress IPv4 WARP gratis adalah **kolam NAT bersama** di colo (Jakarta).
  Undiannya miring — satu alamat bisa sangat dominan — jadi keunikan
  **tidak bisa dipaksakan dalam satu ledakan**; keeper mengundi ulang
  instance yang dobel **bertahap** (anggaran per siklus) sampai konvergen.
  IPv6 egress selalu unik per akun.
- Cloudflare **membatasi laju registrasi device** (±10 per ledakan, lalu
  menetes). Semua registrasi di warpool diberi jarak minimum + backoff.
- Egress bersifat *sticky* (bertahan melewati restart), tapi bukan reservasi
  permanen — karena itu pool di router dirujuk lewat **URL proxy yang tetap**
  (`socks5://warp-N:1080`), bukan lewat IP. Rotasi akun tidak pernah
  mengubah konfigurasi router.

## Perintah

| Perintah | Fungsi |
|---|---|
| `warpool provision` | Bangun/perbaiki armada: daftarkan akun yang belum ada, jalankan container, sinkronkan pools. |
| `warpool check` | Satu siklus pemeriksaan; mencetak tabel status. Kode keluar = jumlah instance bermasalah. |
| `warpool daemon` | Keeper berkala (mode normal lewat docker compose). |
| `warpool rotate <n>` | Registrasi ulang instance `n` sampai egress-nya berganti. |

Setiap siklus menulis ringkasan ke `$WARPOOL_BASE_DIR/warpool-status.json`.
Log hanya berisi kejadian (penyembuhan, pool baru, kegagalan) — siklus yang
sehat tidak berisik.

## Konfigurasi (environment)

| Variable | Default | Keterangan |
|---|---|---|
| `WARPOOL_COUNT` | `30` | Jumlah instance (warp-1..warp-N) |
| `WARPOOL_BASE_DIR` | `/srv/apps/warpgo` | Direktori kerja armada (akun di `usque/acctN/`) |
| `WARPOOL_IMAGE` | `warpool-usque:latest` | Image container instance |
| `WARPOOL_NETWORK` | `public-net` | Jaringan docker bersama router |
| `WARPOOL_BASE_PORT` | `1080` | Port host instance pertama (instance N → +N−1) |
| `WARPOOL_PROBE_HOST` | *(kosong)* | Isi (mis. `127.0.0.1`) untuk probe via port host, bukan nama container |
| `USQUE_BIN` | `/usr/local/bin/usque` | Lokasi binary usque |
| `ROUTER_URL` | *(kosong)* | Base URL 9router-go; kosong = sinkron pools dimatikan |
| `ROUTER_PASSWORD` | *(kosong)* | Password admin router |
| `ROUTER_ENV_FILE` | `/srv/apps/9router-go/.env` | Fallback: baca `INITIAL_PASSWORD` dari file env router |
| `CHECK_INTERVAL` | `15m` | Jeda antar siklus daemon |
| `REG_MIN_GAP` | `20s` | Jarak minimum antar registrasi akun |
| `HEAL_MAX_REGISTER` | `3` | Maksimum registrasi ulang per siklus keeper |

## Deploy

```bash
# 1. bangun image instance (sekali)
docker build -f Dockerfile.usque -t warpool-usque:latest .

# 2. jalankan manajer (membawa docker compose di repo ini)
docker compose up -d --build

# 3. pantau
docker logs -f warpool
docker compose run --rm warpool check
```

Manajer butuh akses ke `/var/run/docker.sock` dan direktori armada yang
di-mount pada **path yang sama** dengan host (bind mount container instance
dirujuk dari sisi host). File `.env` router di-mount read-only hanya untuk
membaca `INITIAL_PASSWORD` saat login API — password tidak pernah ditulis
ke mana pun.

## Keamanan

- `config.json` akun usque berisi **kunci privat device** — jangan pernah
  commit (sudah dicakup `.gitignore`), simpan dengan permission `600`.
- Manajer memegang docker socket (= root di host). Jalankan hanya di host
  tepercaya, jangan ekspos port apa pun milik manajer.
- Port SOCKS5 instance di host (`1080+`) tanpa autentikasi; cukup untuk
  jaringan tepercaya/docker internal. Konsumen utama (9router-go) menjangkau
  instance lewat nama container di jaringan docker, bukan port host.

## Pengembangan

```bash
go build ./...   # butuh Go 1.23+
go vet ./...
```

Struktur: `main.go` (CLI), `config.go` (env), `fleet.go` (docker + registrasi
usque), `probe.go` (pengukuran egress via SOCKS5), `router.go` (klien API
9router-go), `keeper.go` (siklus periksa + penyembuhan + daemon).

Lisensi: MIT.
