package services

import (
	"fmt"
	"strings"
)

func resolveInstanceSSHUser(instanceExtra map[string]string) string {
	if instanceExtra == nil {
		return ""
	}
	return strings.TrimSpace(instanceExtra["ssh_user"])
}

func resolveRemoteExecutionOSUserForPrecheck(ctx *backupTaskPrecheckContext, configuredOSUser string) (string, map[string]string, string, error) {
	effectiveOSUser := strings.TrimSpace(configuredOSUser)
	if effectiveOSUser != "" {
		return effectiveOSUser, ctx.RuntimeInspect, "user_config", nil
	}
	if ctx != nil && ctx.Instance != nil {
		if instanceSSHUser := resolveInstanceSSHUser(parseExtraConfig(ctx.Instance.ExtraConfig)); instanceSSHUser != "" {
			return instanceSSHUser, ctx.RuntimeInspect, "instance_ssh_user", nil
		}
	}
	parsed, _, err := ensureLogicalSourceRuntimeInspection(ctx)
	if err != nil {
		return "", nil, "", fmt.Errorf("未配置 os_user，且无法自动识别数据库进程用户或回退数据源 ssh_user: %w", err)
	}
	if parsed != nil {
		ctx.RuntimeInspect = parsed
	}
	effectiveOSUser = strings.TrimSpace(parsed["PROCESS_USER"])
	if effectiveOSUser == "" {
		return "", parsed, "", fmt.Errorf("未配置 os_user，且既未配置数据源 ssh_user，也未识别到数据库进程用户")
	}
	return effectiveOSUser, parsed, "runtime_process_user", nil
}

func resolveLocalExecutionOSUser(configuredOSUser string, instanceExtra map[string]string, detectedOSUser string) (string, string) {
	if text := strings.TrimSpace(configuredOSUser); text != "" {
		return text, "user_config"
	}
	if text := resolveInstanceSSHUser(instanceExtra); text != "" {
		return text, "instance_ssh_user"
	}
	if text := strings.TrimSpace(detectedOSUser); text != "" {
		return text, "detected_db_process_user"
	}
	return "", ""
}
