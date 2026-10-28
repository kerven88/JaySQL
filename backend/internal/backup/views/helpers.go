package views

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

func parseUint64Param(c *gin.Context, name string) (uint64, bool) {
	raw := c.Param(name)
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		response.ValidateFail(c, "非法参数: "+name)
		return 0, false
	}
	return id, true
}

func parseStringParam(c *gin.Context, name string) (string, bool) {
	value := strings.TrimSpace(c.Param(name))
	if value == "" {
		response.ValidateFail(c, "非法参数: "+name)
		return "", false
	}
	return value, true
}

func hideCredentials(payload any) any {
	if payload == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return payload
	}
	var tmp any
	if e := json.Unmarshal(raw, &tmp); e != nil {
		return payload
	}
	return redactValue(tmp)
}

func redactValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			low := strings.ToLower(k)
			if strings.Contains(low, "password") || strings.Contains(low, "private_key") ||
				strings.Contains(low, "secret") || strings.Contains(low, "access_key") ||
				strings.Contains(low, "credential") || strings.Contains(low, "token") {
				out[k] = maskScalar(val)
			} else {
				out[k] = redactValue(val)
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, it := range x {
			out = append(out, redactValue(it))
		}
		return out
	default:
		return v
	}
}

func maskScalar(val any) any {
	switch v := val.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(v) == "" {
			return ""
		}
		return "**** (已配置)"
	case bool:
		return v
	default:
		s := fmt.Sprintf("%v", v)
		if strings.TrimSpace(s) == "" {
			return v
		}
		return "**** (已配置)"
	}
}
