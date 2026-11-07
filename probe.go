package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// ProbeResult adalah hasil pengukuran egress satu instance.
type ProbeResult struct {
	OK   bool
	IPv4 string
	IPv6 string
	Err  string
}

var (
	ip4Re = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)
	ip6Re = regexp.MustCompile(`^[0-9a-fA-F:]{2,}$`)
)

func socksClient(addr string) (*http.Client, error) {
	d, err := proxy.SOCKS5("tcp", addr, nil, proxy.Direct)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
			if cd, ok := d.(proxy.ContextDialer); ok {
				return cd.DialContext(ctx, network, a)
			}
			return d.Dial(network, a)
		},
	}
	return &http.Client{Transport: tr, Timeout: 25 * time.Second}, nil
}

func fetchText(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// probeInstance mengukur egress instance lewat SOCKS5-nya.
// Tunnel usque bersifat malas (baru menyambung saat ada trafik), jadi
// percobaan pertama sekaligus memanaskan tunnel, lalu diulang sekali.
func probeInstance(cfg Config, n int) ProbeResult {
	client, err := socksClient(cfg.ProbeAddr(n))
	if err != nil {
		return ProbeResult{Err: err.Error()}
	}
	var v4 string
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(5 * time.Second)
		}
		if s, err := fetchText(client, "https://ipv4.icanhazip.com"); err == nil && ip4Re.MatchString(s) {
			v4 = s
			break
		}
	}
	if v4 == "" {
		return ProbeResult{Err: "egress IPv4 tidak terukur"}
	}
	res := ProbeResult{OK: true, IPv4: v4}
	if s, err := fetchText(client, "https://ipv6.icanhazip.com"); err == nil && ip6Re.MatchString(s) && strings.Contains(s, ":") {
		res.IPv6 = s
	}
	return res
}
