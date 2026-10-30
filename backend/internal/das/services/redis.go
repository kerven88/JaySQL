package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/redisreader"
	"github.com/simajiong/jaysql/pkg/secret"
)

type redisReadClient interface {
	Read(string, ...string) (redisreader.Value, error)
	ReadMany([][]string) ([]redisreader.Value, error)
}
type RedisKeyInfo struct {
	Key        string `json:"key"`
	KeyBase64  string `json:"key_base64"`
	Type       string `json:"type"`
	TTLSeconds int64  `json:"ttl_seconds"`
}
type RedisScanResult struct {
	Keys       []RedisKeyInfo `json:"keys"`
	NextCursor string         `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
}
type RedisData struct {
	Encoding  string `json:"encoding"`
	Data      string `json:"data"`
	Truncated bool   `json:"truncated"`
}
type RedisValueRow struct {
	Field       *string   `json:"field,omitempty"`
	FieldBase64 string    `json:"field_base64,omitempty"`
	Value       RedisData `json:"value"`
	Index       *int64    `json:"index,omitempty"`
	Score       string    `json:"score,omitempty"`
}
type RedisValueResult struct {
	RedisKeyInfo
	Size       int64           `json:"size"`
	Exists     bool            `json:"exists"`
	Value      *RedisData      `json:"value,omitempty"`
	Rows       []RedisValueRow `json:"rows"`
	NextCursor string          `json:"next_cursor"`
	NextOffset int64           `json:"next_offset"`
	HasMore    bool            `json:"has_more"`
	Message    string          `json:"message,omitempty"`
}

func redisPageCount(count int) int {
	if count == 0 {
		return 100
	}
	return count
}
func redisCursor(cursor string) (string, error) {
	if cursor == "" {
		return "0", nil
	}
	value, err := strconv.ParseUint(cursor, 10, 64)
	if err != nil {
		return "", fmt.Errorf("Redis 游标必须为无符号十进制整数")
	}
	return strconv.FormatUint(value, 10), nil
}
func redisDisplay(value string) string {
	if !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return strconv.QuoteToASCII(value)
	}
	return value
}
func redisData(value string) RedisData {
	if utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) < 0 {
		return RedisData{Encoding: "utf8", Data: value}
	}
	return RedisData{Encoding: "base64", Data: base64.StdEncoding.EncodeToString([]byte(value))}
}
func redisInfo(key, kind string, ttl int64) RedisKeyInfo {
	return RedisKeyInfo{Key: redisDisplay(key), KeyBase64: base64.StdEncoding.EncodeToString([]byte(key)), Type: kind, TTLSeconds: ttl}
}
func openRedisForRead(ctx context.Context, username, instanceID string, db int) (*redisreader.Client, error) {
	if err := CheckDataAccess(DataAccessRequest{Username: username, InstanceID: instanceID, Schema: strconv.Itoa(db), Action: DataActionSelect, RequireWholeDatabase: true}); err != nil {
		return nil, err
	}
	var instance commonModels.InsightInstances
	if err := global.App.DB.Where("instance_id = ?", instanceID).Take(&instance).Error; err != nil {
		return nil, fmt.Errorf("Redis 实例不存在")
	}
	if !strings.EqualFold(string(instance.DbType), "Redis") {
		return nil, fmt.Errorf("该实例不是 Redis")
	}
	password, err := secret.Decrypt(instance.Password)
	if err != nil {
		return nil, fmt.Errorf("Redis 凭据解密失败")
	}
	var cfg struct {
		TLS bool `json:"redis_tls"`
	}
	if len(instance.ExtraConfig) > 0 {
		if err := json.Unmarshal(instance.ExtraConfig, &cfg); err != nil {
			return nil, fmt.Errorf("Redis TLS 配置无效")
		}
	}
	return redisreader.Dial(ctx, redisreader.Options{Host: instance.Hostname, Port: instance.Port, Username: instance.User, Password: password, TLS: cfg.TLS, Database: db})
}
func ScanRedisKeys(ctx context.Context, username string, form forms.RedisScanForm) (RedisScanResult, error) {
	if _, err := redisCursor(form.Cursor); err != nil {
		return RedisScanResult{}, err
	}
	client, err := openRedisForRead(ctx, username, form.InstanceID, form.DB)
	if err != nil {
		return RedisScanResult{}, err
	}
	defer client.Close()
	return scanRedisKeys(client, form)
}
func scanRedisKeys(client redisReadClient, form forms.RedisScanForm) (RedisScanResult, error) {
	result := RedisScanResult{Keys: []RedisKeyInfo{}, NextCursor: "0"}
	cursor, err := redisCursor(form.Cursor)
	if err != nil {
		return result, err
	}
	count := redisPageCount(form.Count)
	if count < 1 || count > 200 {
		return result, fmt.Errorf("Redis 每页扫描数量必须在 1~200 之间")
	}
	pattern := form.Pattern
	if pattern == "" {
		pattern = "*"
	}
	response, err := client.Read("SCAN", cursor, "MATCH", pattern, "COUNT", strconv.Itoa(count))
	if err != nil {
		return result, err
	}
	if response.Kind != '*' || len(response.Array) != 2 || response.Array[1].Kind != '*' {
		return result, fmt.Errorf("Redis SCAN 响应格式无效")
	}
	result.NextCursor, err = redisCursor(response.Array[0].Text)
	if err != nil {
		return result, err
	}
	result.HasMore = result.NextCursor != "0"
	keys := response.Array[1].Array
	commands := make([][]string, 0, len(keys)*2)
	for _, key := range keys {
		if key.Kind != '$' {
			return result, fmt.Errorf("Redis Key 响应格式无效")
		}
		commands = append(commands, []string{"TYPE", key.Text}, []string{"TTL", key.Text})
	}
	if len(commands) == 0 {
		return result, nil
	}
	metadata, err := client.ReadMany(commands)
	if err != nil {
		return result, err
	}
	if len(metadata) != len(commands) {
		return result, fmt.Errorf("Redis Key 元数据响应无效")
	}
	for i, key := range keys {
		kind, ttl := metadata[2*i], metadata[2*i+1]
		if kind.Kind != '+' || ttl.Kind != ':' {
			return result, fmt.Errorf("Redis Key 元数据类型无效")
		}
		result.Keys = append(result.Keys, redisInfo(key.Text, kind.Text, ttl.Integer))
	}
	return result, nil
}
func ReadRedisValue(ctx context.Context, username string, form forms.RedisValueForm) (RedisValueResult, error) {
	if form.KeyBase64 == nil {
		return RedisValueResult{}, fmt.Errorf("缺少 Redis Key 标识")
	}
	key, err := base64.StdEncoding.DecodeString(*form.KeyBase64)
	if err != nil || len(key) > 65536 {
		return RedisValueResult{}, fmt.Errorf("Redis Key 标识无效或超过 64 KiB")
	}
	if _, err := redisCursor(form.Cursor); err != nil {
		return RedisValueResult{}, err
	}
	client, err := openRedisForRead(ctx, username, form.InstanceID, form.DB)
	if err != nil {
		return RedisValueResult{}, err
	}
	defer client.Close()
	return readRedisValue(client, string(key), form)
}
func readRedisValue(client redisReadClient, key string, form forms.RedisValueForm) (RedisValueResult, error) {
	result := RedisValueResult{Rows: []RedisValueRow{}, NextCursor: "0"}
	cursor, err := redisCursor(form.Cursor)
	if err != nil {
		return result, err
	}
	count := redisPageCount(form.Count)
	if count < 1 || count > 200 || form.Offset < 0 || form.Offset > 536870912 {
		return result, fmt.Errorf("Redis 分页参数无效")
	}
	metadata, err := client.ReadMany([][]string{{"TYPE", key}, {"TTL", key}})
	if err != nil {
		return result, err
	}
	if len(metadata) != 2 || metadata[0].Kind != '+' || metadata[1].Kind != ':' {
		return result, fmt.Errorf("Redis Key 元数据无效")
	}
	kind := metadata[0].Text
	result.RedisKeyInfo = redisInfo(key, kind, metadata[1].Integer)
	result.Exists = kind != "none" && metadata[1].Integer != -2
	if !result.Exists {
		result.Message = "Key 已过期或不存在"
		return result, nil
	}
	sizeCommand := map[string]string{"string": "STRLEN", "hash": "HLEN", "list": "LLEN", "set": "SCARD", "zset": "ZCARD", "stream": "XLEN"}[kind]
	if sizeCommand == "" {
		result.Message = "当前 Key 类型暂不支持内容预览"
		return result, nil
	}
	size, err := client.Read(sizeCommand, key)
	if err != nil {
		return result, err
	}
	if size.Kind != ':' {
		return result, fmt.Errorf("Redis Key 大小响应无效")
	}
	result.Size = size.Integer
	switch kind {
	case "string":
		end := form.Offset + 65535
		value, err := client.Read("GETRANGE", key, strconv.FormatInt(form.Offset, 10), strconv.FormatInt(end, 10))
		if err != nil {
			return result, err
		}
		if value.Kind != '$' {
			return result, fmt.Errorf("Redis 字符串响应无效")
		}
		data := redisData(value.Text)
		result.NextOffset = form.Offset + int64(len(value.Text))
		result.HasMore = result.NextOffset < result.Size && result.NextOffset > form.Offset
		data.Truncated = form.Offset > 0 || result.HasMore
		result.Value = &data
	case "list":
		values, err := client.Read("LRANGE", key, strconv.FormatInt(form.Offset, 10), strconv.FormatInt(form.Offset+int64(count)-1, 10))
		if err != nil {
			return result, err
		}
		if values.Kind != '*' {
			return result, fmt.Errorf("Redis 列表响应无效")
		}
		for i, value := range values.Array {
			result.Rows = append(result.Rows, RedisValueRow{Index: redisIndexPointer(form.Offset + int64(i)), Value: redisData(value.Text)})
		}
		result.NextOffset = form.Offset + int64(len(values.Array))
		result.HasMore = result.NextOffset < result.Size && result.NextOffset > form.Offset
	case "hash", "set", "zset":
		command := map[string]string{"hash": "HSCAN", "set": "SSCAN", "zset": "ZSCAN"}[kind]
		values, err := client.Read(command, key, cursor, "COUNT", strconv.Itoa(count))
		if err != nil {
			return result, err
		}
		if values.Kind != '*' || len(values.Array) != 2 || values.Array[1].Kind != '*' {
			return result, fmt.Errorf("Redis 集合响应无效")
		}
		result.NextCursor, err = redisCursor(values.Array[0].Text)
		if err != nil {
			return result, err
		}
		result.HasMore = result.NextCursor != "0"
		entries := values.Array[1].Array
		if kind != "set" && len(entries)%2 != 0 {
			return result, fmt.Errorf("Redis 集合字段响应无效")
		}
		for i := 0; i < len(entries); i++ {
			if kind == "set" {
				result.Rows = append(result.Rows, RedisValueRow{Value: redisData(entries[i].Text)})
				continue
			}
			name, value := entries[i].Text, entries[i+1].Text
			i++
			if kind == "hash" {
				result.Rows = append(result.Rows, RedisValueRow{Field: redisFieldPointer(redisDisplay(name)), FieldBase64: base64.StdEncoding.EncodeToString([]byte(name)), Value: redisData(value)})
			} else {
				result.Rows = append(result.Rows, RedisValueRow{Value: redisData(name), Score: value})
			}
		}
	case "stream":
		result.Message = "Stream 内容预览暂未开放，已显示类型、TTL 和条目数"
	}
	return result, nil
}

func redisIndexPointer(value int64) *int64   { return &value }
func redisFieldPointer(value string) *string { return &value }
