package connection

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// TransportConfig contains configuration for HTTP transport
type TransportConfig struct {
	// Connection pool settings
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	MaxConnsPerHost     int
	IdleConnTimeout     time.Duration

	// Timeouts
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	KeepAlive             time.Duration
	ExpectContinueTimeout time.Duration

	// TLS
	InsecureSkipVerify bool
	TLSMinVersion      uint16

	// HTTP
	DisableCompression bool
	DisableKeepAlives  bool
	ForceAttemptHTTP2  bool
}

// DefaultTransportConfig returns optimal transport configuration
func DefaultTransportConfig() *TransportConfig {
	return &TransportConfig{
		// Connection pooling - CRITICAL for performance
		MaxIdleConns:        1000, // Global limit
		MaxIdleConnsPerHost: 10,   // Per-host for reuse
		MaxConnsPerHost:     0,    // Unlimited (controlled by rate limiter)
		IdleConnTimeout:     90 * time.Second,

		// Timeouts
		DialTimeout:           5 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		KeepAlive:             30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,

		// TLS
		InsecureSkipVerify: false,
		TLSMinVersion:      tls.VersionTLS12,

		// HTTP
		DisableCompression: false,
		DisableKeepAlives:  false, // CRITICAL: must be false for connection reuse
		ForceAttemptHTTP2:  false, // HTTP/1.1 more predictable for proxies
	}
}

// NewTransport creates optimized HTTP transport for proxy
func NewTransport(proxyURL *url.URL, config *TransportConfig) *http.Transport {
	if config == nil {
		config = DefaultTransportConfig()
	}

	// Create dialer with TCP optimizations
	dialer := &net.Dialer{
		Timeout:   config.DialTimeout,
		KeepAlive: config.KeepAlive,

		// Control function for TCP socket options
		Control: socketControl,
	}

	transport := &http.Transport{
		Proxy:       http.ProxyURL(proxyURL),
		DialContext: dialer.DialContext,

		// Connection pool settings
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxIdleConnsPerHost,
		MaxConnsPerHost:     config.MaxConnsPerHost,
		IdleConnTimeout:     config.IdleConnTimeout,

		// Timeouts
		TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
		ResponseHeaderTimeout: config.ResponseHeaderTimeout,
		ExpectContinueTimeout: config.ExpectContinueTimeout,

		// TLS configuration
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: config.InsecureSkipVerify,
			MinVersion:         config.TLSMinVersion,
		},

		// CRITICAL: Enable connection reuse
		DisableKeepAlives:  config.DisableKeepAlives,
		DisableCompression: config.DisableCompression,
		ForceAttemptHTTP2:  config.ForceAttemptHTTP2,
	}

	return transport
}

// socketControl sets TCP socket options for optimal performance
func socketControl(network, address string, c syscall.RawConn) error {
	var sockErr error

	err := c.Control(func(fd uintptr) {
		// TCP_NODELAY - disable Nagle's algorithm for lower latency
		if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1); err != nil {
			sockErr = err
			return
		}

		// SO_KEEPALIVE - enable TCP keepalive
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1); err != nil {
			sockErr = err
			return
		}

		// TCP keepalive settings (Linux-specific)
		// These may fail on non-Linux systems, so we ignore errors

		// TCP_KEEPIDLE - time before sending first keepalive probe (30 seconds)
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE, 30)

		// TCP_KEEPINTVL - interval between keepalive probes (10 seconds)
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, 10)

		// TCP_KEEPCNT - number of keepalive probes before giving up (3)
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_KEEPCNT, 3)

		// SO_REUSEADDR - allow reuse of local addresses
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	})

	if err != nil {
		return err
	}

	return sockErr
}

// TransportStats contains transport statistics
type TransportStats struct {
	IdleConns int
}

// GetTransportStats returns transport statistics (for monitoring)
func GetTransportStats(t *http.Transport) TransportStats {
	// Note: http.Transport doesn't expose idle connection count directly
	// This is a placeholder for potential future implementation
	return TransportStats{
		IdleConns: 0, // Not directly accessible
	}
}
