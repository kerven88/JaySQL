package services

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
)

// gzipFileTo 以 gzip 压缩 srcPath 并写入 dstPath。
// 供非流式（本地落盘再传输）回退路径使用，压缩级别由任务级选项决定，与流式链路保持一致。
func gzipFileTo(srcPath string, dstPath string, level int) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("打开源文件失败: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}

	gz, err := gzip.NewWriterLevel(dst, level)
	if err != nil {
		_ = dst.Close()
		return fmt.Errorf("初始化 gzip 写入器失败: %w", err)
	}
	if _, err := io.Copy(gz, src); err != nil {
		_ = gz.Close()
		_ = dst.Close()
		return fmt.Errorf("压缩数据失败: %w", err)
	}
	if err := gz.Close(); err != nil {
		_ = dst.Close()
		return fmt.Errorf("关闭 gzip 写入器失败: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("关闭目标文件失败: %w", err)
	}
	return nil
}
