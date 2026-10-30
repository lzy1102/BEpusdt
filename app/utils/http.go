package utils

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MaxCallbackResponseBytes 商户回调响应体上限，避免恶意回调地址返回超大响应耗尽内存
const MaxCallbackResponseBytes int64 = 64 << 10

var (
	callbackAllowMu   sync.RWMutex
	callbackAllowNets []*net.IPNet
)

// SetCallbackPrivateAllowlist 设置允许回调的私网地址白名单。
// 支持 IPv4/IPv6 单个地址与 CIDR，使用逗号、分号、空格或换行分隔。
// 空字符串表示不放开任何私网地址；出于安全考虑，/0 网段会被忽略。
func SetCallbackPrivateAllowlist(raw string) {
	nets := make([]*net.IPNet, 0)
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', ' ', '\t', '\r', '\n':

			return true
		}

		return false
	})

	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {

			continue
		}

		if !strings.Contains(item, "/") {
			ip := net.ParseIP(item)
			if ip == nil {

				continue
			}
			if ip.To4() != nil {
				item += "/32"
			} else {
				item += "/128"
			}
		}

		_, network, err := net.ParseCIDR(item)
		if err != nil {

			continue
		}
		ones, _ := network.Mask.Size()
		if ones == 0 { // 拒绝 0.0.0.0/0、::/0 之类的全网放行
			continue
		}

		nets = append(nets, network)
	}

	callbackAllowMu.Lock()
	callbackAllowNets = nets
	callbackAllowMu.Unlock()
}

func privateAllowed(ip net.IP) bool {
	callbackAllowMu.RLock()
	defer callbackAllowMu.RUnlock()

	for _, network := range callbackAllowNets {
		if network.Contains(ip) {

			return true
		}
	}

	return false
}

// NewCallbackClient 创建用于访问商户回调地址的 HTTP 客户端。
// 回调地址由商户提供，必须限制内网地址并禁止无限重定向，防止 SSRF。
func NewCallbackClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           safeDialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}

	return &http.Client{
		Timeout:   time.Second * 10,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {

				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {

				return errors.New("unsupported redirect scheme")
			}

			return nil
		},
	}
}

func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {

		return nil, err
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {

		return nil, err
	}

	var lastErr error
	for _, ipAddr := range ips {
		if !isPublicIP(ipAddr.IP) && !privateAllowed(ipAddr.IP) {
			lastErr = errors.New("blocked non-public callback address")

			continue
		}

		dialer := &net.Dialer{Timeout: 10 * time.Second}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
		if err == nil {

			return conn, nil
		}

		lastErr = err
	}

	if lastErr == nil {
		lastErr = errors.New("callback host has no public address")
	}

	return nil, lastErr
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() {

		return false
	}

	// 允许回环地址：商户回调与收银台同机部署是常见用法（如本机/同容器内的发卡站）。
	// 仅阻止内网、链路本地、组播与云元数据等危险目标。
	if ip.IsLoopback() {

		return true
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {

		return false
	}

	// Carrier-grade NAT 100.64.0.0/10
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {

			return false
		}
	}

	return true
}

// ReadCallbackBody 读取回调响应体，超过上限时只保留前 MaxCallbackResponseBytes 字节
func ReadCallbackBody(body io.Reader) ([]byte, error) {

	return io.ReadAll(io.LimitReader(body, MaxCallbackResponseBytes))
}
