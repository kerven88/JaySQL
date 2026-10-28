package mysql

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// 内置 gh-ost（官方 v1.1.11 release，SHA256 已与官方 SHA256SUMS 比对）。
// 静态链接二进制，发布服务时随程序分发，执行前释放到用户缓存目录，无需在服务器上单独安装。
var (
	//go:embed embedded/linux-amd64/gh-ost
	ghostBinaryAmd64 []byte
	//go:embed embedded/linux-arm64/gh-ost
	ghostBinaryArm64 []byte
)

func embeddedGhostBinary() ([]byte, bool) {
	switch runtime.GOARCH {
	case "amd64":
		return ghostBinaryAmd64, true
	case "arm64":
		return ghostBinaryArm64, true
	default:
		return nil, false
	}
}

// EnsureEmbeddedGhostBinary 把内置 gh-ost 释放到本地缓存目录（加执行位）并返回路径。
// 已释放且大小一致时直接复用；写入用临时文件 + rename，避免半截二进制。
func EnsureEmbeddedGhostBinary() (string, error) {
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("内置 gh-ost 仅提供 linux 版本（当前系统 %s）：请在服务端安装 gh-ost，或修正 config.yaml 的 ghost.path", runtime.GOOS)
	}
	bin, ok := embeddedGhostBinary()
	if !ok {
		return "", fmt.Errorf("内置 gh-ost 不支持当前 CPU 架构 %s", runtime.GOARCH)
	}
	baseDir, err := os.UserCacheDir()
	if err != nil || baseDir == "" {
		baseDir = os.TempDir()
	}
	target := filepath.Join(baseDir, "goinsight", "gh-ost")
	if fi, serr := os.Stat(target); serr == nil && fi.Mode().IsRegular() && fi.Size() == int64(len(bin)) {
		return target, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("创建 gh-ost 释放目录失败: %w", err)
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return "", fmt.Errorf("释放内置 gh-ost 失败: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("释放内置 gh-ost 失败: %w", err)
	}
	return target, nil
}
