package qmicore

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestResolveIPv4WithTCPDNS(t *testing.T) {
	dnsAddr := startTestTCPDNSServer(t, map[string][]string{
		"probe.local.": {"127.0.0.1", "127.0.0.2"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ips, err := resolveIPv4WithTCPDNS(ctx, "probe.local", []string{dnsAddr}, &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatalf("resolveIPv4WithTCPDNS() error = %v", err)
	}
	if len(ips) != 2 || ips[0] != "127.0.0.1" || ips[1] != "127.0.0.2" {
		t.Fatalf("resolveIPv4WithTCPDNS() ips = %v, want [127.0.0.1 127.0.0.2]", ips)
	}
}

func TestGetPublicIPNoCacheUsesCustomResolver(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("203.0.113.55"))
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("url.Parse(server.URL) error = %v", err)
	}

	dnsAddr := startTestTCPDNSServer(t, map[string][]string{
		"probe.local.": {parsedURL.Hostname()},
	})

	manager := &Manager{
		cfg: config.DeviceConfig{},
		publicIPLookup: func(ctx context.Context, host string) ([]string, error) {
			return resolveIPv4WithTCPDNS(ctx, host, []string{dnsAddr}, &net.Dialer{Timeout: time.Second})
		},
	}

	originalURLs := ipCheckURLs
	ipCheckURLs = []string{fmt.Sprintf("http://probe.local:%s", parsedURL.Port())}
	defer func() {
		ipCheckURLs = originalURLs
	}()

	if got := manager.GetPublicIPNoCache(); got != "203.0.113.55" {
		t.Fatalf("GetPublicIPNoCache() = %q, want %q", got, "203.0.113.55")
	}
}

// TestGetPublicIPv4AndV6NoCacheDualStack 复现并验证修复：在双栈模式下，公网 IP 探测
// 不应被 IPv6 抢跑导致 IPv4 探测结果永远拿不到。两个地址族必须各自独立探测成功。
func TestGetPublicIPv4AndV6NoCacheDualStack(t *testing.T) {
	v4Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("203.0.113.55"))
	}))
	defer v4Server.Close()

	v6Listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback available: %v", err)
	}
	v6Server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("2001:db8::1"))
	}))
	v6Server.Listener = v6Listener
	v6Server.Start()
	defer v6Server.Close()

	v4URL, err := url.Parse(v4Server.URL)
	if err != nil {
		t.Fatalf("url.Parse(v4Server.URL) error = %v", err)
	}
	v6URL, err := url.Parse(v6Server.URL)
	if err != nil {
		t.Fatalf("url.Parse(v6Server.URL) error = %v", err)
	}

	manager := &Manager{
		cfg: config.DeviceConfig{IPVersion: "v4v6"},
		publicIPLookup: func(ctx context.Context, host string) ([]string, error) {
			switch host {
			case "probe4.local":
				return []string{"127.0.0.1"}, nil
			case "probe6.local":
				return []string{"::1"}, nil
			}
			return nil, fmt.Errorf("unexpected host %q", host)
		},
		hasIPv6Bearer: func() bool { return true },
	}

	originalURLs := ipCheckURLs
	ipCheckURLs = []string{
		fmt.Sprintf("http://probe4.local:%s", v4URL.Port()),
		fmt.Sprintf("http://probe6.local:%s", v6URL.Port()),
	}
	defer func() {
		ipCheckURLs = originalURLs
	}()

	gotV4, gotV6 := manager.GetPublicIPv4AndV6NoCache()
	if gotV4 != "203.0.113.55" {
		t.Errorf("GetPublicIPv4AndV6NoCache() v4 = %q, want %q", gotV4, "203.0.113.55")
	}
	if gotV6 != "2001:db8::1" {
		t.Errorf("GetPublicIPv4AndV6NoCache() v6 = %q, want %q", gotV6, "2001:db8::1")
	}
}

// TestGetPublicIPv4AndV6NoCacheSkipsV6WithoutBearer 复现并验证修复：配置允许 v6
// (IPVersion=v4v6) 但 v6 数据承载实际未建立(网络拒绝 PDP type，如 ESM cause #50)时，
// 不应再发起一轮专门的 v6 族探测(其自身有独立的 10s 超时，且会重新对所有 ipCheckURLs
// 发起请求)，否则每轮刷新都白白多等一次注定失败的探测、拖慢刷新并占用退避重试。
// 注意：probePublicIP 本身会对所有 ipCheckURLs 并发竞速、由拨号阶段按族过滤结果，
// 因此 v4 探测中仍会解析到 probe6.local 一次（解析结果会被过滤掉），这不属于本测试要禁止的
// "专门的 v6 探测轮次"，故按调用次数断言而非"零调用"。
func TestGetPublicIPv4AndV6NoCacheSkipsV6WithoutBearer(t *testing.T) {
	v4Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("203.0.113.55"))
	}))
	defer v4Server.Close()

	v4URL, err := url.Parse(v4Server.URL)
	if err != nil {
		t.Fatalf("url.Parse(v4Server.URL) error = %v", err)
	}

	var mu sync.Mutex
	v6LookupCalls := 0

	manager := &Manager{
		cfg: config.DeviceConfig{IPVersion: "v4v6"},
		publicIPLookup: func(ctx context.Context, host string) ([]string, error) {
			switch host {
			case "probe4.local":
				return []string{"127.0.0.1"}, nil
			case "probe6.local":
				mu.Lock()
				v6LookupCalls++
				mu.Unlock()
				return nil, fmt.Errorf("no v6 route")
			}
			return nil, fmt.Errorf("unexpected host %q", host)
		},
		hasIPv6Bearer: func() bool { return false },
	}

	originalURLs := ipCheckURLs
	ipCheckURLs = []string{
		fmt.Sprintf("http://probe4.local:%s", v4URL.Port()),
		"http://probe6.local:1",
	}
	defer func() {
		ipCheckURLs = originalURLs
	}()

	gotV4, gotV6 := manager.GetPublicIPv4AndV6NoCache()
	if gotV4 != "203.0.113.55" {
		t.Errorf("GetPublicIPv4AndV6NoCache() v4 = %q, want %q", gotV4, "203.0.113.55")
	}
	if gotV6 != "" {
		t.Errorf("GetPublicIPv4AndV6NoCache() v6 = %q, want empty (v6 bearer down)", gotV6)
	}

	mu.Lock()
	calls := v6LookupCalls
	mu.Unlock()
	// 仅 v4 族探测竞速过程中触发的一次解析；若仍有独立的 v6 族探测轮次，会再触发一次（=2）。
	if calls != 1 {
		t.Errorf("probe6.local lookup called %d times, want 1 (no dedicated v6-family probe pass)", calls)
	}
}

func TestExtractAAAARecords(t *testing.T) {
	records := []dns.RR{
		&dns.AAAA{
			Hdr: dns.RR_Header{
				Name:   "probe.local.",
				Rrtype: dns.TypeAAAA,
				Class:  dns.ClassINET,
				Ttl:    30,
			},
			AAAA: net.ParseIP("2001:db8::1"),
		},
		&dns.A{
			Hdr: dns.RR_Header{
				Name:   "probe.local.",
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    30,
			},
			A: net.ParseIP("127.0.0.1").To4(),
		},
	}
	ips := extractAAAARecords(records)
	if len(ips) != 1 || ips[0] != "2001:db8::1" {
		t.Fatalf("extractAAAARecords() = %v, want [2001:db8::1]", ips)
	}
}

func startTestTCPDNSServer(t *testing.T, records map[string][]string) string {
	t.Helper()

	mux := dns.NewServeMux()
	mux.HandleFunc(".", func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		for _, question := range req.Question {
			if question.Qtype != dns.TypeA {
				continue
			}
			for _, ip := range records[question.Name] {
				resp.Answer = append(resp.Answer, &dns.A{
					Hdr: dns.RR_Header{
						Name:   question.Name,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    30,
					},
					A: net.ParseIP(ip).To4(),
				})
			}
		}
		_ = w.WriteMsg(resp)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}

	server := &dns.Server{
		Listener: listener,
		Net:      "tcp",
		Handler:  mux,
	}
	go func() {
		_ = server.ActivateAndServe()
	}()

	t.Cleanup(func() {
		_ = server.Shutdown()
	})

	return listener.Addr().String()
}
