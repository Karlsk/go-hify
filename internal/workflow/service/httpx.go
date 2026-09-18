package service

// httpx：api 节点的出站 HTTP client（spec 06 §4.2 / O6）。
// SSRF 防护在建连时（Dialer.Control）而非 URL 解析时——DNS rebinding 场景下解析结果
// 可以在解析后、连接前被换掉，唯有 connect 时刻的 IP 才可信；重定向由 client 复用
// 同一 Transport 拨号，每跳天然复验。

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// errSSRFBlocked 出站目标命中禁止段的哨兵（经 %w 链上抛，executor 归一为
// WORKFLOW_EXECUTION_FAILED）。
var errSSRFBlocked = errors.New("outbound address blocked by SSRF policy")

// isBlockedIP 判定目标 IP 是否命中禁止段。
// 恒禁（不受 BLOCK_PRIVATE 影响）：loopback / link-local（含云元数据 169.254.169.254）/
// IPv6 ULA（fc00::/7）/ 未指定地址 / 多播；
// blockPrivate=true 时 RFC1918 私网一并拒绝（默认放行：api 节点主场景是内网自签服务）。
func isBlockedIP(ip net.IP, blockPrivate bool) bool {
	if ip == nil {
		return true // 解析失败按禁处理（fail-closed）
	}
	blocked := ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || isIPv6ULA(ip)
	if blockPrivate {
		blocked = blocked || ip.IsPrivate()
	}
	return blocked
}

// isIPv6ULA 判定 IPv6 唯一本地地址（fc00::/7，前 7 位 1111110）。
func isIPv6ULA(ip net.IP) bool {
	if v6 := ip.To16(); v6 != nil && len(v6) == net.IPv6len {
		return v6[0]&0xfe == 0xfc
	}
	return false
}

// outboundClient api 节点专用出站 client：仅 http/https（scheme 校验在 executor，
// client 层不重复）、SSRF 建连时校验、redirect 每跳复验（同 Transport 拨号）、
// timeout_sec 归一（0 → 默认 10s）、ssl_verify 映射 TLS 校验开关。
func outboundClient(timeoutSec int, sslVerify, blockPrivate bool) *http.Client {
	timeout := 10 * time.Second
	if timeoutSec > 0 {
		timeout = time.Duration(timeoutSec) * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("ssrf check %s: %w", address, err)
			}
			if ip := net.ParseIP(host); isBlockedIP(ip, blockPrivate) {
				return fmt.Errorf("dial %s: %w", address, errSSRFBlocked)
			}
			return nil
		},
	}
	tr := &http.Transport{
		DialContext:         dialer.DialContext,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: !sslVerify}, //nolint:gosec // O6：ssl_verify 配置面（内网自签默认跳过）
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}
