package utils

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	BizIDPrefixInstance  = "ds-"
	BizIDPrefixBackup    = "bk-"
	BizIDPrefixBackupSet = "bks-"
	BizIDPrefixTicket    = "tk-"
	BizIDPrefixOrderTask = "ot-"
	BizIDPrefixRestore   = "rs-"
	BizIDPrefixCloudAcct = "ca-"
	// 大模型接入模块
	BizIDPrefixLlmProvider = "lp-"
	BizIDPrefixLlmModel    = "md-"
	BizIDPrefixLlmTemplate = "vt-"
	// 用户反馈
	BizIDPrefixFeedback = "fb-"
	// 细粒度数据授权
	BizIDPrefixDataGrant = "dg-"
	// 数据访问权限申请（工单维度提权）
	BizIDPrefixAccessRequest = "ra-"
)

const bizIDAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

func generateBizIDRandom(prefix string) string {
	const randomLen = 12
	buf := make([]byte, randomLen)
	if _, err := rand.Read(buf); err == nil {
		out := make([]byte, 0, len(prefix)+randomLen)
		out = append(out, []byte(prefix)...)
		for _, b := range buf {
			out = append(out, bizIDAlphabet[int(b)%len(bizIDAlphabet)])
		}
		return string(out)
	}
	var seed int64
	seedBuf := make([]byte, 8)
	if _, err := rand.Read(seedBuf); err == nil {
		seed = int64(binary.BigEndian.Uint64(seedBuf))
	} else {
		seed = time.Now().UnixNano()
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s%d", prefix, seed)))
	return prefix + strings.ToLower(hex.EncodeToString(sum[:6]))
}

func GenerateInstanceID() string {
	return generateBizIDRandom(BizIDPrefixInstance)
}

func GenerateBackupTaskID() string {
	return generateBizIDRandom(BizIDPrefixBackup)
}

func GenerateBackupSetID() string {
	return generateBizIDRandom(BizIDPrefixBackupSet)
}

func GenerateOrderID() string {
	return generateBizIDRandom(BizIDPrefixTicket)
}

func GenerateOrderTaskID() string {
	return generateBizIDRandom(BizIDPrefixOrderTask)
}

func GenerateRestoreTaskID() string {
	return generateBizIDRandom(BizIDPrefixRestore)
}

func GenerateCloudAccountID() string {
	return generateBizIDRandom(BizIDPrefixCloudAcct)
}

// GenerateLlmProviderID 大模型服务商配置业务 ID
func GenerateLlmProviderID() string {
	return generateBizIDRandom(BizIDPrefixLlmProvider)
}

// GenerateLlmModelID 大模型清单业务 ID
func GenerateLlmModelID() string {
	return generateBizIDRandom(BizIDPrefixLlmModel)
}

// GenerateLlmTemplateID 厂商模板业务 ID（内置模板用固定值，自定义模板用此生成）
func GenerateLlmTemplateID() string {
	return generateBizIDRandom(BizIDPrefixLlmTemplate)
}

// GenerateFeedbackID 用户反馈业务 ID
func GenerateFeedbackID() string {
	return generateBizIDRandom(BizIDPrefixFeedback)
}

// GenerateDataGrantID 细粒度数据授权单号
func GenerateDataGrantID() string {
	return generateBizIDRandom(BizIDPrefixDataGrant)
}

// GenerateAccessRequestID 数据访问权限申请单号
func GenerateAccessRequestID() string {
	return generateBizIDRandom(BizIDPrefixAccessRequest)
}

func IsValidBizID(value string) bool {
	v := strings.TrimSpace(value)
	if len(v) != 15 {
		return false
	}
	if !strings.HasPrefix(v, BizIDPrefixInstance) &&
		!strings.HasPrefix(v, BizIDPrefixBackup) &&
		!strings.HasPrefix(v, BizIDPrefixBackupSet) &&
		!strings.HasPrefix(v, BizIDPrefixTicket) &&
		!strings.HasPrefix(v, BizIDPrefixOrderTask) &&
		!strings.HasPrefix(v, BizIDPrefixRestore) &&
		!strings.HasPrefix(v, BizIDPrefixCloudAcct) &&
		!strings.HasPrefix(v, BizIDPrefixLlmProvider) &&
		!strings.HasPrefix(v, BizIDPrefixLlmModel) &&
		!strings.HasPrefix(v, BizIDPrefixLlmTemplate) &&
		!strings.HasPrefix(v, BizIDPrefixFeedback) &&
		!strings.HasPrefix(v, BizIDPrefixAccessRequest) {
		return false
	}
	for i := 3; i < len(v); i++ {
		c := v[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}
	return true
}
