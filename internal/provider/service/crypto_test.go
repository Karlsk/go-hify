package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// crypto 测试：AES-256-GCM 加解密往返、认证失败路径、打码边界。
// 主密钥形态对齐 config.ProviderCfg.MasterKey（32 字节，PROVIDER_MASTER_KEY base64 解码而来）。

var testMaster = []byte("0123456789abcdef0123456789abcdef") // 32B

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	enc, err := encryptAPIKey(testMaster, "sk-proj-abcdefgh1234")
	require.NoError(t, err)

	pt, err := decryptAPIKey(testMaster, enc)
	require.NoError(t, err)
	assert.Equal(t, "sk-proj-abcdefgh1234", pt)
}

// nonce 随机：同一明文两次加密产生不同密文（都能解回原文）。
func TestEncrypt_UniqueNonce(t *testing.T) {
	e1, err := encryptAPIKey(testMaster, "same-plaintext")
	require.NoError(t, err)
	e2, err := encryptAPIKey(testMaster, "same-plaintext")
	require.NoError(t, err)
	assert.NotEqual(t, e1, e2, "nonce 应随机，密文不应相同")

	for _, e := range []string{e1, e2} {
		pt, err := decryptAPIKey(testMaster, e)
		require.NoError(t, err)
		assert.Equal(t, "same-plaintext", pt)
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	enc, err := encryptAPIKey(testMaster, "sk-xxx")
	require.NoError(t, err)
	// 篡改末字符（保持 base64 字符集内）。
	tampered := enc[:len(enc)-1] + "A"
	if tampered == enc {
		tampered = enc[:len(enc)-1] + "B"
	}
	_, err = decryptAPIKey(testMaster, tampered)
	assert.Error(t, err, "GCM 认证失败应报错")
}

func TestDecrypt_WrongMasterKey(t *testing.T) {
	enc, err := encryptAPIKey(testMaster, "sk-xxx")
	require.NoError(t, err)
	_, err = decryptAPIKey([]byte("ffffffffffffffffffffffffffffffff"), enc)
	assert.Error(t, err)
}

func TestDecrypt_MalformedInput(t *testing.T) {
	_, err := decryptAPIKey(testMaster, "!!!not-base64!!!")
	assert.Error(t, err)

	_, err = decryptAPIKey(testMaster, "QUJD") // base64("ABC")，短于 nonce
	assert.Error(t, err)
}

func TestEncrypt_BadMasterKeyLength(t *testing.T) {
	// 20B 非 AES 规范长度（16/24/32）；32B 的强约束由 config 启动期校验，这里只验加密层报错。
	_, err := encryptAPIKey(make([]byte, 20), "sk-xxx")
	assert.Error(t, err)
}

func TestMaskAPIKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sk-proj-abcdefgh1234", "sk-p…1234"},
		{"123456789", "1234…6789"}, // 恰好 9 位，取首尾各 4
		{"12345678", "****"},       // ≤8 位全遮蔽
		{"k", "****"},
		{"", "****"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, maskAPIKey(c.in), "mask(%q)", c.in)
	}
	// 打码不包含中间段：中间字符不出现在结果里（首尾 4 字符除外）。
	masked := maskAPIKey("sk-MIDDLESECRET-9999")
	assert.False(t, strings.Contains(masked, "MIDDLESECRET"))
}
