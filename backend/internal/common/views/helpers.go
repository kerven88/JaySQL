package views

import (
	"strconv"
	"strings"

	"github.com/simajiong/jaysql/pkg/response"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/gin-gonic/gin"
)

func getUsername(c *gin.Context) (string, bool) {
	claims := jwt.ExtractClaims(c)
	raw, ok := claims["id"]
	if !ok {
		response.Fail(c, "用户信息不存在")
		return "", false
	}
	username, ok := raw.(string)
	if !ok || username == "" {
		response.Fail(c, "用户信息不存在")
		return "", false
	}
	return username, true
}

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
