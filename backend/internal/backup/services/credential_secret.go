package services

import (
	"strings"

	"github.com/simajiong/jaysql/pkg/secret"
)

// ============================================================================
// 备份模块凭据读写的统一入口
//
// 背景：备份链路里有一批凭据此前是明文落库的（备份节点 ssh_password/ssh_private_key、
// 数据源 extra_config 里的同名字段），修复方式是「写入加密 + 读取解密」。
// 但读取点散落在十几个文件里，每处都手写一遍 secret.Decrypt + 错误处理既啰嗦又容易漏，
// 收敛到这里：一处定义，全局调用。
//
// 幂等语义（迁移期必须）：secret.Decrypt 遇到非密文（历史明文）原样返回，
// 所以「先改代码、后迁数据」也能正确工作，不需要停机等迁移完成。
// ============================================================================

// decryptCredential 解密凭据。密文→明文；历史明文原样返回；解密失败退回原值（不中断主流程）。
func decryptCredential(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	plain, err := secret.Decrypt(trimmed)
	if err != nil {
		return trimmed
	}
	return plain
}

// encryptCredential 加密凭据。已是密文则原样返回（幂等，杜绝双重加密）。
func encryptCredential(value string) (string, error) {
	return secret.Encrypt(strings.TrimSpace(value))
}
