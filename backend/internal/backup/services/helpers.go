package services

import (
	"encoding/json"
	"errors"
	"fmt"

	jwt "github.com/appleboy/gin-jwt/v2"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func currentUsername(c *gin.Context) string {
	if c == nil {
		return ""
	}
	claims := jwt.ExtractClaims(c)
	raw, ok := claims["id"]
	if !ok {
		return ""
	}
	username, ok := raw.(string)
	if !ok {
		return ""
	}
	return username
}

func loadInstanceByID(id uint64) (*commonModels.InsightInstances, error) {
	var instance commonModels.InsightInstances
	if err := global.App.DB.Where("id = ?", id).First(&instance).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("实例不存在")
		}
		return nil, err
	}
	if instance.Password != "" {
		if plain, err := secret.Decrypt(instance.Password); err == nil && plain != "" {
			instance.Password = plain
		}
	}
	return &instance, nil
}

func parseExtraConfig(extra datatypes.JSON) map[string]string {
	if len(extra) == 0 {
		return map[string]string{}
	}
	var raw map[string]any
	if err := json.Unmarshal(extra, &raw); err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	for k, v := range raw {
		if v == nil {
			continue
		}
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}
