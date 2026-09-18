package service

// httpx 测试（spec 06 O6 拍板矩阵）：SSRF 恒禁三段（loopback / link-local / IPv6 ULA）、
// RFC1918 默认放行、BLOCK_PRIVATE 一键全禁、建连时拦截（真实 Dialer.Control 路径）、
// 重定向每跳复验（首跳 stub 应答 302、跳目标走真实 SSRF transport 拨号被拒）。
// 零真实网络：拦截矩阵为纯函数断言，出站断言只打不可达的 IP 字面量。

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		name         string
		ip           string
		blockPrivate bool
		want         bool
	}{
		// 恒禁三段（不受 BLOCK_PRIVATE 影响，O6）
		{"loopback 127.0.0.1", "127.0.0.1", false, true},
		{"loopback 127.8.8.8（127/8 全段）", "127.8.8.8", false, true},
		{"loopback ::1", "::1", false, true},
		{"link-local 169.254.1.1（云元数据段）", "169.254.1.1", false, true},
		{"link-local fe80::1", "fe80::1", false, true},
		{"ULA fc00::1", "fc00::1", false, true},
		{"ULA fd00::1", "fd00::1", false, true},
		// RFC1918 默认放行（O6：api 节点主场景 = 内网自签服务）
		{"RFC1918 10/8 默认放行", "10.0.0.1", false, false},
		{"RFC1918 172.16/12 默认放行", "172.16.0.1", false, false},
		{"RFC1918 192.168/16 默认放行", "192.168.1.1", false, false},
		{"公网放行", "8.8.8.8", false, false},
		{"IPv6 公网放行", "2001:4860:4860::8888", false, false},
		// BLOCK_PRIVATE=true：私网一并拒绝，恒禁段不变
		{"BLOCK_PRIVATE 10/8 拒", "10.0.0.1", true, true},
		{"BLOCK_PRIVATE 172.16/12 拒", "172.16.0.1", true, true},
		{"BLOCK_PRIVATE 192.168/16 拒", "192.168.1.1", true, true},
		{"BLOCK_PRIVATE 公网仍放行", "8.8.8.8", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			require.NotNil(t, ip, "测试 IP 字面量非法: %s", tc.ip)
			assert.Equal(t, tc.want, isBlockedIP(ip, tc.blockPrivate))
		})
	}
}

// 建连时拦截（Dialer.Control，防 DNS rebinding 的关键位置）：loopback 目标在拨号前
// 即被拒，错误链可经 errors.Is 追溯到 errSSRFBlocked。
func TestOutboundClientDialBlocked(t *testing.T) {
	c := outboundClient(2, false, false)
	_, err := c.Get("http://127.0.0.1:1/x")
	require.Error(t, err)
	assert.ErrorIs(t, err, errSSRFBlocked)
}

// oneShotRT 首个请求直接返回预置响应（不拨号），后续请求委托真实 transport——
// 用于证明重定向目标地址重新走 SSRF 保护的拨号路径（每跳复验）。
type oneShotRT struct {
	resp   *http.Response
	next   http.RoundTripper
	served bool
}

func (t *oneShotRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if !t.served {
		t.served = true
		t.resp.Request = r
		return t.resp, nil
	}
	return t.next.RoundTrip(r)
}

// 重定向每跳复验：首跳 stub 应答 302 → 禁止目标（loopback）；第二跳走真实
// SSRF transport，建连时被 Control 拦截——证明 redirect 没有绕过防护。
func TestOutboundClientRedirectRecheck(t *testing.T) {
	c := outboundClient(2, false, false)
	real := c.Transport
	c.Transport = &oneShotRT{
		resp: &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://127.0.0.1:9/redirected"}},
			Body:       io.NopCloser(strings.NewReader("")),
		},
		next: real,
	}
	_, err := c.Get("http://192.168.1.1/origin") // 首跳不拨号（stub 应答），私网默认放行
	require.Error(t, err)
	assert.ErrorIs(t, err, errSSRFBlocked)
}

// timeout 归一（0 → 默认 10s）与 ssl_verify → TLS 配置映射（O6：内网自签默认跳过校验）。
func TestOutboundClientDefaults(t *testing.T) {
	assert.Equal(t, 10*time.Second, outboundClient(0, false, false).Timeout, "timeout_sec 0 → 默认 10s")
	assert.Equal(t, 7*time.Second, outboundClient(7, false, false).Timeout)

	tr := outboundClient(3, false, false).Transport.(*http.Transport)
	assert.True(t, tr.TLSClientConfig.InsecureSkipVerify, "ssl_verify=false → 跳过证书校验")

	trVerify := outboundClient(3, true, false).Transport.(*http.Transport)
	assert.False(t, trVerify.TLSClientConfig.InsecureSkipVerify, "ssl_verify=true → 校验证书")
}
