package secret

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Mode 透明拦截模式。
type Mode int

const (
	// ModeOff 插件不生效，只保留 L1 显式调用。
	// 这是默认值，也是迁移第一阶段应处的状态。
	ModeOff Mode = iota
	// ModeShadow 只解密不加密，用于验证清单是否完整：
	// 若某字段解密出来是乱码或空，说明清单漏登记或漏迁移。
	ModeShadow
	// ModeOn 完整生效：写入自动加密，读取自动解密。
	ModeOn
)

var transparent struct {
	mu   sync.RWMutex
	mode Mode
}

// SetMode 切换透明拦截模式（运行时可调，便于灰度与紧急回滚）。
func SetMode(m Mode) {
	transparent.mu.Lock()
	transparent.mode = m
	transparent.mu.Unlock()
}

// CurrentMode 返回当前透明拦截模式。
func CurrentMode() Mode {
	transparent.mu.RLock()
	defer transparent.mu.RUnlock()
	return transparent.mode
}

type transparentConfig struct {
	mode Mode
}

// TransparentOption 透明拦截配置项。
type TransparentOption func(*transparentConfig)

// WithTransparentMode 指定拦截模式。
func WithTransparentMode(m Mode) TransparentOption {
	return func(c *transparentConfig) { c.mode = m }
}

// Install 注册 GORM 透明拦截插件。
//
// 必须在任何业务查询发生之前完成注册，否则会漏掉启动期的读取。
// 默认 ModeOff：本次改造只落地能力，开关留给迁移阶段逐步打开。
func Install(db *gorm.DB, opts ...TransparentOption) error {
	if db == nil {
		return nil
	}
	cfg := &transparentConfig{mode: ModeOff}
	for _, opt := range opts {
		opt(cfg)
	}
	SetMode(cfg.mode)

	// 幂等：已注册过就跳过，避免重复 Install 时同一段逻辑被挂两遍。
	// （gorm 的 callback 类型是未导出的，无法用变量承载，只能内联。）
	const encryptName = "secret:encrypt"
	const decryptName = "secret:decrypt"

	if db.Callback().Create().Get(encryptName) == nil {
		if err := db.Callback().Create().Before("gorm:before_create").Register(encryptName, encryptCallback); err != nil {
			return err
		}
	}
	if db.Callback().Update().Get(encryptName) == nil {
		if err := db.Callback().Update().Before("gorm:before_update").Register(encryptName, encryptCallback); err != nil {
			return err
		}
	}
	if db.Callback().Query().Get(decryptName) == nil {
		if err := db.Callback().Query().After("gorm:after_query").Register(decryptName, decryptCallback); err != nil {
			return err
		}
	}
	return nil
}

// encryptCallback 写入前按清单加密。
func encryptCallback(tx *gorm.DB) {
	if CurrentMode() != ModeOn || tx.Error != nil || tx.Statement.Schema == nil {
		return
	}
	specs := FieldsFor(tx.Statement.Schema.Table)
	if len(specs) == 0 {
		return
	}
	// map 形态（Updates(map[string]any{})）：直接改 map
	if dest, ok := tx.Statement.Dest.(map[string]any); ok {
		for _, spec := range specs {
			key := spec.Column
			value, ok := dest[key]
			if !ok {
				continue
			}
			next, err := transformValue(value, spec, Encrypt)
			if err != nil || next == nil {
				continue
			}
			dest[key] = next
		}
		return
	}
	reflectValue := reflect.Indirect(reflect.ValueOf(tx.Statement.Dest))
	applyToStruct(statementContext(tx), reflectValue, tx.Statement.Schema, specs, Encrypt)
}

// decryptCallback 查询后按清单解密并写回模型字段。
func decryptCallback(tx *gorm.DB) {
	mode := CurrentMode()
	if mode == ModeOff || tx.Error != nil || tx.Statement.Schema == nil {
		return
	}
	specs := FieldsFor(tx.Statement.Schema.Table)
	if len(specs) == 0 {
		return
	}
	reflectValue := reflect.Indirect(reflect.ValueOf(tx.Statement.Dest))
	applyToStruct(statementContext(tx), reflectValue, tx.Statement.Schema, specs, Decrypt)
}

// statementContext 取当前语句的 context；为空时退回 Background，
// 避免因为 context 缺失导致整段拦截被跳过。
func statementContext(tx *gorm.DB) context.Context {
	if tx.Statement != nil && tx.Statement.Context != nil {
		return tx.Statement.Context
	}
	return context.Background()
}

func applyToStruct(ctx context.Context, reflectValue reflect.Value, sch *schema.Schema, specs []FieldSpec, fn func(string) (string, error)) {
	switch reflectValue.Kind() {
	case reflect.Struct:
		applyOne(ctx, reflectValue, sch, specs, fn)
	case reflect.Slice, reflect.Array:
		for i := 0; i < reflectValue.Len(); i++ {
			item := reflect.Indirect(reflect.ValueOf(reflectValue.Index(i).Addr().Interface()))
			if item.Kind() == reflect.Struct {
				applyOne(ctx, item, sch, specs, fn)
			}
		}
	default:
	}
}

func applyOne(ctx context.Context, reflectValue reflect.Value, sch *schema.Schema, specs []FieldSpec, fn func(string) (string, error)) {
	for _, spec := range specs {
		field := sch.LookUpField(spec.Column)
		if field == nil {
			continue
		}
		// 登记了 JSONPath 的列（无论列类型是 datatypes.JSON 还是 string）都按 JSON 处理
		if len(spec.JSONPath) > 0 {
			applyJSONField(ctx, reflectValue, field, spec, fn)
			continue
		}
		fieldValue := field.ReflectValueOf(ctx, reflectValue)
		if !fieldValue.CanSet() {
			continue
		}
		current, ok := fieldValue.Interface().(string)
		if !ok {
			continue
		}
		next, err := fn(current)
		if err != nil || next == current {
			continue
		}
		if err := field.Set(ctx, reflectValue, next); err != nil {
			continue
		}
	}
}

// applyJSONField 处理 datatypes.JSON / []byte / json.RawMessage 形态的 JSON 列。
func applyJSONField(ctx context.Context, reflectValue reflect.Value, field *schema.Field, spec FieldSpec, fn func(string) (string, error)) {
	fieldValue := field.ReflectValueOf(ctx, reflectValue)
	var raw []byte
	switch typed := fieldValue.Interface().(type) {
	case []byte:
		raw = typed
	case json.RawMessage:
		raw = typed
	case string:
		raw = []byte(typed)
	default:
		return
	}
	if len(raw) == 0 {
		return
	}
	if len(spec.JSONPath) == 0 || fn == nil {
		return
	}
	next, err := applyJSONPaths(raw, spec.JSONPath, fn)
	if err != nil || len(next) == 0 {
		return
	}
	switch fieldValue.Interface().(type) {
	case json.RawMessage:
		_ = field.Set(ctx, reflectValue, json.RawMessage(next))
	case []byte:
		_ = field.Set(ctx, reflectValue, next)
	default:
		_ = field.Set(ctx, reflectValue, string(next))
	}
}

// transformValue 处理 map 更新里的值（string 或 []byte）。
func transformValue(value any, spec FieldSpec, fn func(string) (string, error)) (any, error) {
	switch typed := value.(type) {
	case string:
		next, err := fn(typed)
		if err != nil {
			return nil, err
		}
		return next, nil
	case []byte:
		if len(spec.JSONPath) == 0 || len(typed) == 0 {
			return value, nil
		}
		return applyJSONPaths(typed, spec.JSONPath, fn)
	case json.RawMessage:
		if len(spec.JSONPath) == 0 || len(typed) == 0 {
			return value, nil
		}
		return applyJSONPaths(typed, spec.JSONPath, fn)
	default:
		return value, nil
	}
}
