package secret

import (
	"reflect"
	"strings"

	"github.com/sirupsen/logrus"
)

// sensitiveKeys 判定「这个 key 看起来装的是凭据」的依据。
//
// 用于日志脱敏与接口导出前的兜底扫描：新增日志/字段时即便忘了显式脱敏，
// 只要 key 名命中这里就会被自动打码。
var sensitiveKeys = []string{
	"password", "passwd", "pwd",
	"secret", "api_key", "apikey", "access_key", "app_secret",
	"private_key", "ssh_key", "token", "authorization", "mail_password",
}

// IsSensitiveKey 判断 key 名是否为敏感凭据键（大小写、下划线、连字符不敏感）。
func IsSensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.NewReplacer("-", "", "_", "", " ", "").Replace(normalized)
	for _, k := range sensitiveKeys {
		if strings.Contains(normalized, strings.NewReplacer("-", "", "_", "", " ", "").Replace(k)) {
			return true
		}
	}
	return false
}

// Mask 统一掩码。空值返回空，非空返回固定掩码 —— 不用等长星号，避免泄露密码长度。
func Mask(s string) string {
	if s == "" {
		return ""
	}
	return DefaultMask
}

// MaskStruct 按 key 名反射脱敏，用于接口返回/导出前的统一处理。
//
// 支持 struct、map[string]any、切片与指针；命中敏感 key 的字符串值替换为掩码，
// 其余字段原样保留。返回值的形态与入参一致（原对象会被就地修改）。
func MaskStruct(v any) any {
	maskValue(reflect.ValueOf(v))
	return v
}

func maskValue(rv reflect.Value) {
	if !rv.IsValid() {
		return
	}
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return
		}
		maskValue(rv.Elem())
	case reflect.Struct:
		rt := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			field := rt.Field(i)
			if field.PkgPath != "" { // 未导出字段跳过
				continue
			}
			target := rv.Field(i)
			if IsSensitiveKey(field.Name) && target.Kind() == reflect.String && target.CanSet() {
				target.SetString(Mask(target.String()))
				continue
			}
			maskValue(target)
		}
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return
		}
		for _, key := range rv.MapKeys() {
			value := rv.MapIndex(key)
			if IsSensitiveKey(key.String()) && value.Kind() == reflect.String {
				rv.SetMapIndex(key, reflect.ValueOf(Mask(value.String())).Convert(value.Type()))
				continue
			}
			if value.Kind() == reflect.Interface && !value.IsNil() {
				maskValue(value.Elem())
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			maskValue(rv.Index(i))
		}
	default:
	}
}

// logHook 日志脱敏钩子：对 entry.Data 里命中敏感 key 名的值自动替换为掩码。
//
// 只处理结构化字段（Data），日志正文里的字符串无法可靠改写 ——
// 因此「不要把凭据拼进日志文本」仍然是一条硬要求，hook 只是最后一道兜底。
type logHook struct{}

// LogHook 返回日志脱敏钩子，注册方式：
//
//	global.App.Log.AddHook(secret.LogHook())
func LogHook() logrus.Hook { return logHook{} }

func (logHook) Levels() []logrus.Level { return logrus.AllLevels }

func (logHook) Fire(entry *logrus.Entry) error {
	for key, value := range entry.Data {
		if !IsSensitiveKey(key) {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed != "" {
				entry.Data[key] = DefaultMask
			}
		case *string:
			if typed != nil && *typed != "" {
				masked := DefaultMask
				entry.Data[key] = &masked
			}
		}
	}
	return nil
}
