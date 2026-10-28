package secret

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/simajiong/jaysql/internal/global"
)

// 密钥来源，按优先级从高到低：
//
//  1. GOINSIGHT_SECRET_KEY        环境变量（推荐：K8s/Docker 注入，不落配置文件）
//  2. GOINSIGHT_SECRET_KEY_FILE   密钥文件路径（K8s secret / Docker secret 挂载）
//  3. app.secret_key              配置文件（向后兼容，生产建议弃用）
//
// 轮换期额外支持 GOINSIGHT_SECRET_KEY_PREVIOUS / GOINSIGHT_SECRET_KEY_PREVIOUS_FILE：
// 解密时若主密钥失败会自动回退旧密钥，加密一律用新密钥。
const (
	EnvKey          = "GOINSIGHT_SECRET_KEY"
	EnvKeyFile      = "GOINSIGHT_SECRET_KEY_FILE"
	EnvKeyPrevious  = "GOINSIGHT_SECRET_KEY_PREVIOUS"
	EnvKeyPrevFile  = "GOINSIGHT_SECRET_KEY_PREVIOUS_FILE"
	EnvAllowDefault = "GOINSIGHT_SECRET_ALLOW_DEFAULT_KEY"

	// keyLength AES-256 要求密钥恰好 32 字节。
	keyLength = 32
)

// defaultWeakKey 是仓库自带的示例密钥。
// 只要配置了它，等于密钥公开 —— 加密只剩合规意义，没有实际防护作用。
const defaultWeakKey = "A9$k!pZ3@rT7&xQ1#Lf8^Vm2*Ws6%Hd0"

var keyState struct {
	mu        sync.RWMutex
	loaded    bool
	current   []byte
	previous  []byte
	source    string
	weakKey   bool
	warnings  []string
	loadError error
}

// currentKey 取当前主密钥；未加载时惰性加载一次。
//
// 惰性加载让「没走 app.Run 启动流程」的场景（单元测试、一次性工具）也能正常工作，
// 而不要求调用方记得先 LoadKey。
func currentKey() []byte {
	keyState.mu.RLock()
	loaded, cur := keyState.loaded, keyState.current
	keyState.mu.RUnlock()
	if !loaded {
		if err := LoadKey(); err != nil {
			return nil
		}
		keyState.mu.RLock()
		cur = keyState.current
		keyState.mu.RUnlock()
	}
	return cur
}

func previousKey() ([]byte, bool) {
	keyState.mu.RLock()
	defer keyState.mu.RUnlock()
	return keyState.previous, len(keyState.previous) > 0
}

// LoadKey 按优先级加载密钥。可重复调用，用于密钥热加载后的刷新。
func LoadKey() error {
	candidates := []struct {
		source string
		value  string
	}{
		{"env:" + EnvKey, os.Getenv(EnvKey)},
		{"file:" + EnvKeyFile, readKeyFile(os.Getenv(EnvKeyFile))},
		{"config:app.secret_key", configKey()},
	}

	keyState.mu.Lock()
	defer keyState.mu.Unlock()

	keyState.loaded = false
	keyState.current = nil
	keyState.previous = nil
	keyState.source = ""
	keyState.weakKey = false
	keyState.warnings = nil

	for _, c := range candidates {
		value := strings.TrimSpace(c.value)
		if value == "" {
			continue
		}
		if len(value) != keyLength {
			keyState.warnings = append(keyState.warnings,
				fmt.Sprintf("密钥来源 %s 长度不是 %d 字节（实际 %d），已跳过", c.source, keyLength, len(value)))
			continue
		}
		keyState.current = []byte(value)
		keyState.source = c.source
		break
	}

	if keyState.current == nil {
		keyState.loadError = fmt.Errorf("未找到可用密钥：请设置环境变量 %s（32 字节），或在 config.yaml 的 app.secret_key 配置 32 字节密钥", EnvKey)
		return keyState.loadError
	}

	// 旧密钥只用于解密，缺失是正常状态（未处于轮换期）。
	if prev := strings.TrimSpace(os.Getenv(EnvKeyPrevious)); len(prev) == keyLength {
		keyState.previous = []byte(prev)
	} else if p := strings.TrimSpace(readKeyFile(os.Getenv(EnvKeyPrevFile))); len(p) == keyLength {
		keyState.previous = []byte(p)
	}

	keyState.weakKey = string(keyState.current) == defaultWeakKey
	if keyState.weakKey && !isTruthy(os.Getenv(EnvAllowDefault)) {
		keyState.warnings = append(keyState.warnings,
			"当前密钥等于仓库默认示例密钥，等同于公开！请用 GOINSIGHT_SECRET_KEY 注入独立密钥后重启")
	}

	keyState.loaded = true
	keyState.loadError = nil
	return nil
}

// SelfCheck 启动自检：密钥可用性与风险检查。
//
// 返回 error 表示「不能带病运行」—— 密钥缺失或算法往返失败时必须拒绝启动，
// 否则启动后写入的凭据可能是明文或坏密文。默认密钥只告警不阻断：
// 生产环境正在跑，一旦因为配置没跟上就起不来，比用弱密钥更糟。
func SelfCheck() error {
	if err := LoadKey(); err != nil {
		return err
	}
	keyState.mu.RLock()
	warnings := append([]string(nil), keyState.warnings...)
	weak := keyState.weakKey
	source := keyState.source
	keyState.mu.RUnlock()

	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "[secret][WARN] %s\n", w)
	}
	if weak {
		fmt.Fprintf(os.Stderr,
			"[secret][WARN] ============================================================\n"+
				"[secret][WARN] 检测到默认示例密钥！任何拿到仓库的人都能解密库里的凭据。\n"+
				"[secret][WARN] 请生成新密钥：openssl rand -base64 32 | head -c 32\n"+
				"[secret][WARN] 并通过环境变量 %s 注入（设置 %s=true 可关闭本提示）\n"+
				"[secret][WARN] ============================================================\n",
			EnvKey, EnvAllowDefault)
	}

	// 往返测试：确认算法与密钥真的可用，而不是只长度对。
	const probe = "goinsight-secret-self-check"
	cipher, err := Encrypt(probe)
	if err != nil {
		return fmt.Errorf("secret: 自检加密失败: %w", err)
	}
	plain, err := Decrypt(cipher)
	if err != nil {
		return fmt.Errorf("secret: 自检解密失败: %w", err)
	}
	if plain != probe {
		return fmt.Errorf("secret: 自检往返结果不一致")
	}

	fmt.Fprintf(os.Stderr, "[secret] 密钥已加载 source=%s fingerprint=%s\n", source, KeyFingerprint())
	return nil
}

// KeyFingerprint 返回密钥指纹（SHA-256 前 4 字节的十六进制）。
//
// 用途：日志里核对「这次启动用的是不是我以为的那把钥匙」，
// 尤其是密钥轮换后确认新密钥已生效。不含密钥本身，可以安全打印。
func KeyFingerprint() string {
	key := currentKey()
	if len(key) == 0 {
		return ""
	}
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:4])
}

// KeySource 返回当前密钥来源，便于排障「到底用的哪把钥匙」。
func KeySource() string {
	keyState.mu.RLock()
	defer keyState.mu.RUnlock()
	return keyState.source
}

func configKey() string {
	if global.App == nil || global.App.Config.App.SECRET_KEY == "" {
		return ""
	}
	return global.App.Config.App.SECRET_KEY
}

func readKeyFile(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[secret][WARN] 读取密钥文件 %s 失败: %v\n", path, err)
		return ""
	}
	return strings.TrimRight(string(data), "\r\n\t ")
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
