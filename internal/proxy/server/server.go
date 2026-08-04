package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/logger"
	socks5 "github.com/things-go/go-socks5"
)

const (
	ModeSocks5 = "socks5"
	ModeHTTP   = "http"
)

// Server 代表单个代理服务器（SOCKS5 或 HTTP）。
type Server struct {
	ID         string
	Mode       string
	ListenAddr string
	Port       int
	Interface  string

	socksServer *socks5.Server
	httpServer  *http.Server

	// 流量统计
	Stats *TrafficStats

	// 用于优雅关闭
	listener net.Listener
	mu       sync.Mutex
	shutdown bool
}

// New 创建新的代理服务器。
func New(id, mode, listenAddr string, port int, iface string, authEnabled bool, username, password string) (*Server, error) {
	mode = normalizeMode(mode)
	stats := NewTrafficStats()
	if strings.TrimSpace(listenAddr) == "" {
		listenAddr = "0.0.0.0"
	}

	dialer := newBoundDialer(id, iface)

	out := &Server{
		ID:         id,
		Mode:       mode,
		ListenAddr: listenAddr,
		Port:       port,
		Interface:  iface,
		Stats:      stats,
	}

	switch mode {
	case ModeSocks5:
		srv, err := newSocks5Server(dialer, stats, authEnabled, username, password)
		if err != nil {
			return nil, err
		}
		out.socksServer = srv
	case ModeHTTP:
		srv, err := newHTTPProxyServer(id, dialer, stats, authEnabled, username, password)
		if err != nil {
			return nil, err
		}
		out.httpServer = srv
	default:
		return nil, fmt.Errorf("不支持的代理模式: %s", mode)
	}

	return out, nil
}

func normalizeMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		return ModeSocks5
	}
	return m
}

func newSocks5Server(dialer *net.Dialer, stats *TrafficStats, authEnabled bool, username, password string) (*socks5.Server, error) {
	opts := []socks5.Option{
		socks5.WithDial(func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialOutboundConn(ctx, dialer, stats, network, addr)
		}),
	}

	if authEnabled {
		user := strings.TrimSpace(username)
		pass := strings.TrimSpace(password)
		if user == "" || pass == "" {
			return nil, fmt.Errorf("代理鉴权已启用但用户名或密码为空")
		}
		opts = append(opts, socks5.WithAuthMethods([]socks5.Authenticator{
			socks5.UserPassAuthenticator{
				Credentials: socks5.StaticCredentials{user: pass},
			},
		}))
	}

	return socks5.NewServer(opts...), nil
}

func newBoundDialer(id, iface string) *net.Dialer {
	iface = strings.TrimSpace(iface)
	return &net.Dialer{
		Timeout: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			if iface == "" {
				return nil
			}
			var sockErr error
			if err := c.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
			}); err != nil {
				return err
			}
			if sockErr != nil {
				logger.Error(fmt.Sprintf("[%s] 绑定设备失败", id), "iface", iface, "err", sockErr)
				return fmt.Errorf("SO_BINDTODEVICE(%s) 失败: %w", iface, sockErr)
			}
			return nil
		},
	}
}

func dialOutboundConn(ctx context.Context, dialer *net.Dialer, stats *TrafficStats, network, addr string) (net.Conn, error) {
	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	stats.IncrConnection()
	return &countingConn{Conn: conn, stats: stats}, nil
}

// countingConn 包装连接以统计流量。
type countingConn struct {
	net.Conn
	stats  *TrafficStats
	closed bool
	mu     sync.Mutex
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.stats.AddReceived(int64(n))
	}
	return n, err
}

func (c *countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.stats.AddSent(int64(n))
	}
	return n, err
}

func (c *countingConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	c.stats.DecrActiveConn()
	return c.Conn.Close()
}

// 确保实现 io.ReadWriteCloser。
var _ io.ReadWriteCloser = (*countingConn)(nil)

// Start 启动代理服务器。
func (s *Server) Start() error {
	address := net.JoinHostPort(s.ListenAddr, strconv.Itoa(s.Port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("监听失败: %w", err)
	}

	s.mu.Lock()
	s.listener = listener
	s.shutdown = false
	s.mu.Unlock()

	switch s.Mode {
	case ModeSocks5:
		if s.socksServer == nil {
			_ = listener.Close()
			return errors.New("SOCKS5 服务器未初始化")
		}
		err = s.socksServer.Serve(listener)
	case ModeHTTP:
		if s.httpServer == nil {
			_ = listener.Close()
			return errors.New("HTTP 代理服务器未初始化")
		}
		err = s.httpServer.Serve(listener)
	default:
		_ = listener.Close()
		return fmt.Errorf("不支持的代理模式: %s", s.Mode)
	}

	s.mu.Lock()
	closedByShutdown := s.shutdown
	s.listener = nil
	s.mu.Unlock()

	if closedByShutdown {
		if err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return nil
		}
	}
	return err
}

// Shutdown 优雅关闭代理服务器。
func (s *Server) Shutdown() error {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return nil
	}
	s.shutdown = true
	listener := s.listener
	httpSrv := s.httpServer
	s.mu.Unlock()

	var errs []error
	if httpSrv != nil {
		if err := httpSrv.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	if listener != nil {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// IsRunning 检查服务器是否正在运行。
func (s *Server) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listener != nil && !s.shutdown
}

// GetStats 获取流量统计。
func (s *Server) GetStats() map[string]int64 {
	if s.Stats == nil {
		return nil
	}
	return s.Stats.GetStats()
}

// GetFormattedStats 获取格式化的流量统计。
func (s *Server) GetFormattedStats() map[string]string {
	if s.Stats == nil {
		return nil
	}
	raw := s.Stats.GetStats()
	return map[string]string{
		"bytes_sent":     FormatBytes(raw["bytes_sent"]),
		"bytes_received": FormatBytes(raw["bytes_received"]),
		"connections":    fmt.Sprintf("%d", raw["connections"]),
		"active_conns":   fmt.Sprintf("%d", raw["active_conns"]),
	}
}
