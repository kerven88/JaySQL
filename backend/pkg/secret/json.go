package secret

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// EncryptJSON 按路径加密 JSON 内的敏感值，其余字段保持原样。
//
// 设计约束（踩坑预防）：
//  1. 用 json.Decoder + UseNumber() 解码，避免大整数被转成 float64 造成精度丢失；
//  2. 解码为「保序对象」而不是 map —— map 序列化会重排 key，导致 JSON 文本无意义地漂移；
//  3. 只处理 string 类型的值；数字、布尔、对象、数组原样保留；
//  4. 加密后 value 仍是合法 JSON 字符串（enc:v1:...），数据库侧 JSON_EXTRACT 依然可用。
//
// 空输入返回空输入；非法 JSON 返回错误（宁可显式失败，也不要静默丢数据）。
func EncryptJSON(raw []byte, paths []string) ([]byte, error) {
	return applyJSONPaths(raw, paths, func(v string) (string, error) { return Encrypt(v) })
}

// DecryptJSON 按路径解密 JSON 内的敏感值。
//
// 幂等：非密文（历史明文）原样保留，因此存量 extra_config 无需迁移即可读取。
func DecryptJSON(raw []byte, paths []string) ([]byte, error) {
	return applyJSONPaths(raw, paths, func(v string) (string, error) { return Decrypt(v) })
}

// EncryptJSONWith 按清单里登记的 JSONPath 加密（清单为空则该列无需处理）。
func EncryptJSONWith(raw []byte, spec FieldSpec) ([]byte, error) {
	if len(spec.JSONPath) == 0 {
		return raw, nil
	}
	return EncryptJSON(raw, spec.JSONPath)
}

// DecryptJSONWith 按清单里登记的 JSONPath 解密。
func DecryptJSONWith(raw []byte, spec FieldSpec) ([]byte, error) {
	if len(spec.JSONPath) == 0 {
		return raw, nil
	}
	return DecryptJSON(raw, spec.JSONPath)
}

// NeedsEncryption 判断 JSON 内按路径定位的值是否存在「未加密」项。
//
// 用于迁移工具统计真实待迁移量：直接比较整个 JSON 字符串是不可靠的 ——
// 序列化会把缩进格式紧凑化，导致"其实没有敏感值"的行也被判成待迁移。
// 空串不算待加密（Encrypt("") 返回空串，本来也不需要加密）。
func NeedsEncryption(raw []byte, paths []string) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false, nil
	}
	root, err := decodeOrdered(trimmed)
	if err != nil {
		return false, fmt.Errorf("secret: JSON 解析失败: %w", err)
	}
	need := false
	for _, p := range paths {
		segments, err := parsePath(p)
		if err != nil {
			return false, err
		}
		if len(segments) == 0 {
			continue
		}
		_ = collect(root, segments, func(v string) {
			if v != "" && !IsEncrypted(v) {
				need = true
			}
		})
		if need {
			return true, nil
		}
	}
	return need, nil
}

// collect 按路径定位并收集命中的字符串值。
func collect(node any, segments []string, fn func(string)) error {
	if len(segments) == 0 {
		return nil
	}
	head, rest := segments[0], segments[1:]
	switch typed := node.(type) {
	case *orderedObject:
		value, ok := typed.get(head)
		if !ok {
			return nil
		}
		if len(rest) == 0 {
			if text, isStr := value.(string); isStr {
				fn(text)
			}
			return nil
		}
		return collect(value, rest, fn)
	case []any:
		if head != "*" {
			return nil
		}
		for _, item := range typed {
			if err := collect(item, rest, fn); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

// MaskJSON 按路径把 JSON 内的敏感值替换为掩码，用于接口返回前的脱敏。
//
// 与 DecryptJSON 的区别：这里不还原明文，只保证"值不出接口"。
// 典型场景：实例列表返回 extra_config，里面的 ssh_password 是密文 ——
// 密文虽然没有密钥解不开，但合规审计不接受接口直接返回凭据字段。
func MaskJSON(raw []byte, paths []string) ([]byte, error) {
	return applyJSONPaths(raw, paths, func(v string) (string, error) { return Mask(v), nil })
}

// MaskJSONWith 按清单登记的 JSONPath 脱敏。
func MaskJSONWith(raw []byte, spec FieldSpec) ([]byte, error) {
	if len(spec.JSONPath) == 0 {
		return raw, nil
	}
	return MaskJSON(raw, spec.JSONPath)
}

func applyJSONPaths(raw []byte, paths []string, fn func(string) (string, error)) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, nil
	}
	root, err := decodeOrdered(trimmed)
	if err != nil {
		return nil, fmt.Errorf("secret: JSON 解析失败: %w", err)
	}
	for _, p := range paths {
		segments, err := parsePath(p)
		if err != nil {
			return nil, err
		}
		if len(segments) == 0 {
			continue
		}
		if err := walk(root, segments, fn); err != nil {
			return nil, err
		}
	}
	return json.Marshal(root)
}

// parsePath 解析 "$.a.b" / "$.channels[*].secret" 形式的路径。
func parsePath(path string) ([]string, error) {
	p := strings.TrimSpace(path)
	p = strings.TrimPrefix(p, "$")
	p = strings.TrimPrefix(p, ".")
	if p == "" {
		return nil, nil
	}
	// 数组通配：$.channels[*].secret → ["channels", "*", "secret"]
	p = strings.ReplaceAll(p, "[*]", ".*")
	p = strings.ReplaceAll(p, "[", ".")
	p = strings.ReplaceAll(p, "]", "")
	segments := strings.Split(p, ".")
	for i, s := range segments {
		segments[i] = strings.TrimSpace(s)
		if segments[i] == "" {
			return nil, fmt.Errorf("secret: JSON 路径非法: %s", path)
		}
	}
	return segments, nil
}

// walk 按路径定位并对命中位置的字符串值执行 fn。
func walk(node any, segments []string, fn func(string) (string, error)) error {
	if len(segments) == 0 {
		return nil
	}
	head, rest := segments[0], segments[1:]

	switch typed := node.(type) {
	case *orderedObject:
		value, ok := typed.get(head)
		if !ok {
			return nil
		}
		if len(rest) == 0 {
			if text, isStr := value.(string); isStr {
				next, err := fn(text)
				if err != nil {
					return err
				}
				typed.set(head, next)
			}
			return nil
		}
		return walk(value, rest, fn)
	case []any:
		// 数组：只有 "*" 通配可以穿透
		if head != "*" {
			return nil
		}
		for _, item := range typed {
			if err := walk(item, rest, fn); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// 保序 JSON 对象模型
//
// Go 的 map[string]any 序列化时会按 key 排序输出。对 extra_config 这类配置列
// 而言，key 顺序没有业务含义，但「改一个字段导致整个 JSON 文本重排」会让
// diff 与审计噪音变大。这里用一个最小实现保住原始 key 顺序。
// ---------------------------------------------------------------------------

type orderedObject struct {
	keys []string
	vals map[string]any
}

func newOrderedObject() *orderedObject {
	return &orderedObject{vals: map[string]any{}}
}

func (o *orderedObject) get(key string) (any, bool) {
	v, ok := o.vals[key]
	return v, ok
}

func (o *orderedObject) set(key string, value any) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = value
}

func (o *orderedObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')
		valueBytes, err := json.Marshal(o.vals[key])
		if err != nil {
			return nil, err
		}
		buf.Write(valueBytes)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// decodeOrdered 用 Token 流解码，保留对象 key 顺序，数字用 json.Number 承载。
func decodeOrdered(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	value, err := decodeValue(dec, token)
	if err != nil {
		return nil, err
	}
	// 顶层之后不应再有内容（多余内容说明是拼接的畸形 JSON）
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("secret: JSON 尾部存在多余内容")
	}
	return value, nil
}

func decodeValue(dec *json.Decoder, token json.Token) (any, error) {
	delim, ok := token.(json.Delim)
	if !ok {
		// string / json.Number / bool / nil
		return token, nil
	}
	switch delim {
	case '{':
		obj := newOrderedObject()
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("secret: JSON 对象 key 不是字符串")
			}
			valueToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			value, err := decodeValue(dec, valueToken)
			if err != nil {
				return nil, err
			}
			obj.set(key, value)
		}
		if _, err := dec.Token(); err != nil { // 消费 '}'
			return nil, err
		}
		return obj, nil
	case '[':
		arr := []any{}
		for dec.More() {
			itemToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			item, err := decodeValue(dec, itemToken)
			if err != nil {
				return nil, err
			}
			arr = append(arr, item)
		}
		if _, err := dec.Token(); err != nil { // 消费 ']'
			return nil, err
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("secret: 非法的 JSON 分隔符 %v", delim)
	}
}
