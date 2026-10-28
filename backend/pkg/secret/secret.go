// Package secret 提供系统统一的凭据加解密能力。
//
// 设计要点（改造前散落在各业务里手写 utils.Encrypt/Decrypt，漏掉某字段不报错）：
//
//  1. 单一实现：密钥、算法、密文格式只在本包定义，业务代码禁止自行实现 AES。
//  2. 幂等：Encrypt 对已是密文的值原样返回；Decrypt 对非密文原样返回。
//     这两条让「存量明文」与「新密文」能在迁移期共存，无需停机批量处理。
//  3. 可识别：密文统一带 "enc:v1:aesgcm:" 前缀，IsEncrypted 不再靠长度猜。
//  4. 可演进：前缀带版本号，支持算法/密钥轮换与多版本共存。
//
// 密文格式：
//
//	enc:v1:aesgcm:<base64( nonce 12B | ciphertext | tag 16B )>
//	│    │    │
//	│    │    └─ 算法标识
//	│    └────── 版本
//	└─────────── 固定魔数，IsEncrypted 的唯一依据
//
// ⚠️ 部署顺序（生产上踩过，代价是 4 个实例连不上数据库）：
//
//	本包的 Decrypt 能读三种形态：明文（原样返回）、无前缀旧密文（有回退）、
//	带前缀密文。而旧的 utils.Decrypt 只能读前两种 —— 带前缀的一律报错。
//
//	所以迁移（给存量密文补前缀 / 把明文加密）必须放在**新版二进制部署之后**，
//	不能反过来。反过来做 = 旧版在迁移完成的那一刻起全线解密失败。
//	详细顺序见 sql/20261014_secret_ciphertext_prefix.sql 头部。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

const (
	magic = "enc:"

	// VersionCurrent 当前密文版本。算法或密钥轮换时递增，旧版本继续可解密。
	VersionCurrent = "v1"
	// AlgAESGCM AES-256-GCM 算法标识。
	AlgAESGCM = "aesgcm"

	// PrefixV1 当前密文前缀。
	PrefixV1 = magic + VersionCurrent + ":" + AlgAESGCM + ":"

	// PrefixLegacy 通知渠道模块历史使用的前缀（enc:aes:）。
	// 算法与 v1 完全一致，只是缺少版本段，这里保留识别能力，
	// 保证存量渠道配置（insight_monitor_notify_channels.config）解密不失败。
	PrefixLegacy = magic + "aes:"

	// DefaultMask 统一掩码。固定长度，不泄露原始密码长度。
	DefaultMask = "******"
)

var (
	// ErrKeyNotReady 密钥尚未加载（配置缺失或长度不为 32 字节）。
	ErrKeyNotReady = errors.New("secret: 密钥未就绪")
	// ErrMalformed 密文格式不合法（前缀正确但载荷不是合法 base64/长度不足）。
	ErrMalformed = errors.New("secret: 密文格式不合法")
)

// IsEncrypted 判断是否为本模块产出的密文。
//
// 只认前缀，不做长度/字符猜测 —— 这正是加前缀的意义：
// 「密文」与「恰好长得像 base64 的明文」从此可以可靠区分。
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, magic)
}

// Version 返回密文版本（"v1" / "legacy"）；非密文返回空串。
func Version(s string) string {
	switch {
	case strings.HasPrefix(s, PrefixV1):
		return VersionCurrent
	case strings.HasPrefix(s, PrefixLegacy):
		return "legacy"
	default:
		return ""
	}
}

// Encrypt 加密明文。
//
// 幂等：输入为空串返回空串（不产生无意义密文）；输入已是密文则原样返回，
// 杜绝双重加密 —— 这是「写入侧可以无脑调用」的前提。
func Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if IsEncrypted(plain) {
		return plain, nil
	}
	raw, err := SealRaw(plain)
	if err != nil {
		return "", err
	}
	return PrefixV1 + raw, nil
}

// MustEncrypt 用于启动期配置加载等「必须成功」的场景，失败直接 panic。
func MustEncrypt(plain string) string {
	out, err := Encrypt(plain)
	if err != nil {
		panic("secret: 加密失败: " + err.Error())
	}
	return out
}

// Decrypt 解密。
//
// 幂等语义（整个迁移的基石）：非密文（历史明文、或迁移前无前缀的旧密文）
// 原样返回且不报错，这样存量读取侧调用无需修改即可兼容历史数据。
func Decrypt(cipherOrPlain string) (string, error) {
	if cipherOrPlain == "" {
		return "", nil
	}
	payload, ok := trimEnvelope(cipherOrPlain)
	if ok {
		return OpenRaw(payload)
	}
	// 无前缀：可能是历史明文，也可能是 utils.Encrypt 产出的「无前缀旧密文」。
	//
	// 后者按旧格式尝试解密一次：AWS-GCM 的 tag 校验会挡住"恰好长得像 base64 的明文"
	// （解不开就返回原值），所以这个回退是安全的。
	// 收益很实在：即使运维忘了先跑 A 类迁移脚本（补前缀），发版后存量密文依然能解开，
	// 迁移从"不发版就活不了"降级成"格式统一的美化步骤"。
	if LooksLikeCiphertext(cipherOrPlain) {
		if plain, err := OpenRaw(cipherOrPlain); err == nil {
			return plain, nil
		}
	}
	return cipherOrPlain, nil
}

// LooksLikeCiphertext 判断值是否"像"无前缀的旧密文。
//
// 判据来自密文本身的形态：AES-256-GCM 的输出是 nonce(12B)+ciphertext+tag(16B)，
// 最短 28 字节 → base64 后恰好 40 字符、且是 4 的倍数。不满足的一律按明文处理。
//
// 导出给迁移工具与审计脚本使用：密钥校验、残留明文扫描都需要先区分
// "历史明文" 与 "无前缀旧密文" —— 前者要加密，后者只需补前缀。
func LooksLikeCiphertext(s string) bool {
	if len(s) < 40 || len(s)%4 != 0 {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(s)
	return err == nil && len(decoded) >= 28
}

// SealRaw 加密并返回「不带前缀」的裸密文（base64）。
//
// 仅供历史格式（PrefixLegacy）兼容使用：通知渠道的存量配置是 enc:aes:<裸密文>，
// 前缀由该模块自己拼。新代码一律用 Encrypt，不要自己拼前缀。
func SealRaw(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	key := currentKey()
	if len(key) == 0 {
		return "", ErrKeyNotReady
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	// 输出布局：nonce | ciphertext | tag
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// OpenRaw 解密 SealRaw 产出的裸密文（base64）。
//
// 轮换期：主密钥解不开时自动回退到 GOINSIGHT_SECRET_KEY_PREVIOUS 提供的旧密钥，
// 加密始终用新密钥 —— 这就是「平滑轮换」的实现方式。
func OpenRaw(payloadBase64 string) (string, error) {
	key := currentKey()
	if len(key) == 0 {
		return "", ErrKeyNotReady
	}
	data, err := base64.StdEncoding.DecodeString(payloadBase64)
	if err != nil {
		return "", ErrMalformed
	}
	plain, err := openWith(key, data)
	if err != nil {
		if prev, ok := previousKey(); ok {
			if recovered, prevErr := openWith(prev, data); prevErr == nil {
				return recovered, nil
			}
		}
		return "", err
	}
	return plain, nil
}

// openWith 用指定密钥解密原始字节（nonce | ciphertext | tag）。
func openWith(key, data []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", ErrMalformed
	}
	nonce := data[:gcm.NonceSize()]
	plain, err := gcm.Open(nil, nonce, data[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// trimEnvelope 剥掉密文前缀，返回裸载荷；非密文返回 false。
func trimEnvelope(s string) (string, bool) {
	switch {
	case strings.HasPrefix(s, PrefixV1):
		return strings.TrimPrefix(s, PrefixV1), true
	case strings.HasPrefix(s, PrefixLegacy):
		return strings.TrimPrefix(s, PrefixLegacy), true
	default:
		return "", false
	}
}
