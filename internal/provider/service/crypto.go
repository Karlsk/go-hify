package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// API Key 加密（AES-256-GCM）。安全约束（db_model.md §2.2）：
//   - auth_config 只存密文：base64(nonce‖ciphertext) 放在 apiKeyEncryptedKey 键下；
//   - 主密钥 32 字节，来自 config（PROVIDER_MASTER_KEY，env / Docker secrets），禁止入 Git；
//   - 明文只存在于写入前与打码瞬间——不进日志、不进 schema JSON、不进缓存、不进 executions。

// apiKeyEncryptedKey auth_config 内存放密文的键（migrations/00002 的 COMMENT 同款约定）。
//
// 密文不绑定行身份（无 AAD）：provider id 在 Create 时由 DB 插入才产生，加密发生在插入前，
// 按 id 绑定需要两阶段写（插入→回读 id→再 UPDATE 密文），得不偿失；跨行移植密文需要 SQL
// 写权限，该权限下攻击面已远超本项，接受此残余风险。若日后引入密钥版本 / 重加密迁移，
// 顺手把密文升级为 {"v":1,...} 信封结构。
const apiKeyEncryptedKey = "api_key_encrypted"

// encryptAPIKey 加密明文 key，返回 base64(nonce‖ciphertext)。
// 每次加密随机 nonce——同明文密文不同，无长度 / 前缀泄露。
func encryptAPIKey(master []byte, plaintext string) (string, error) {
	aead, err := newAEAD(master)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(plaintext), nil)), nil
}

// decryptAPIKey 解密 encryptAPIKey 的产物。密文被篡改或主密钥不符时返回错误（GCM 认证）；
// 调用方（详情打码）对错误降级为跳过打码，不阻断读取。
func decryptAPIKey(master []byte, encoded string) (string, error) {
	aead, err := newAEAD(master)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode api key: %w", err)
	}
	if len(raw) < aead.NonceSize() {
		return "", errors.New("api key ciphertext too short")
	}
	pt, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt api key: %w", err)
	}
	return string(pt), nil
}

// newAEAD 由 32 字节主密钥构造 AES-256-GCM（master 长度非法时报错）。
func newAEAD(master []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(master)
	if err != nil {
		return nil, fmt.Errorf("init aes cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// maskAPIKey 打码展示：首 4 + … + 尾 4；长度 ≤ 8 全遮蔽。中间段永不出现。
// 按 byte 切（非 rune）：各家 API key 均为 ASCII，非 ASCII 情形极端罕见，接受可能截断。
func maskAPIKey(plaintext string) string {
	if len(plaintext) <= 8 {
		return "****"
	}
	return plaintext[:4] + "…" + plaintext[len(plaintext)-4:]
}
