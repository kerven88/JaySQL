package middleware

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/users/services"
	"github.com/sirupsen/logrus"
)

// abortAuthInvalid token 签名有效、但指向的用户已不存在（被删除 / 同名行全部软删 /
// 迁移后元库没有该账号）时调用。必须回 401 而不是 500：前端 request.js 只对
// 401/422 做清 token + 跳登录，回 500 会把「身份已失效」当成普通接口错误，
// 页面呈现成一片空状态（性能概览 2026-10-24 故障即此症状），用户无从判断要重新登录。
func abortAuthInvalid(c *gin.Context, username string) {
	global.App.Log.WithFields(logrus.Fields{
		"request_id": requestid.Get(c),
		"username":   username,
	}).Warn("认证失效：token 指向的用户不存在")
	c.AbortWithStatusJSON(401, gin.H{"code": 401, "msg": "认证已失效，请重新登录", "data": nil, "request_id": requestid.Get(c)})
}

func hasPermissionCode(username, permission string) (bool, error) {
	codes, err := services.GetPermissionCodesByUsername(username)
	if err != nil {
		return false, err
	}
	if slices.Contains(codes, "*") {
		return true, nil
	}
	return slices.Contains(codes, permission), nil
}

func abortNoPermission(c *gin.Context, username string, permission string) {
	if permission == "" {
		globalMessage := "无权限"
		c.AbortWithStatusJSON(403, gin.H{"code": 403, "msg": globalMessage, "data": nil, "request_id": requestid.Get(c)})
		return
	}
	c.AbortWithStatusJSON(403, gin.H{
		"code":       403,
		"msg":        fmt.Sprintf("缺少权限: %s", permission),
		"data":       nil,
		"request_id": requestid.Get(c),
	})
}

func HasAdminPermission() gin.HandlerFunc {
	return RequirePermission("system.access")
}

func RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		username, ok := GetUserNameFromJWT(c)
		if !ok {
			c.AbortWithStatusJSON(401, gin.H{"code": 401, "msg": "认证信息无效", "data": nil, "request_id": requestid.Get(c)})
			return
		}
		ok, err := hasPermissionCode(username, permission)
		if err != nil {
			if errors.Is(err, services.ErrUserNotFound) {
				abortAuthInvalid(c, username)
				return
			}
			global.App.Log.WithFields(logrus.Fields{"request_id": requestid.Get(c), "username": username, "permission": permission, "error": err}).Error("权限校验失败")
			c.AbortWithStatusJSON(500, gin.H{"code": 500, "msg": "权限校验失败", "data": nil, "request_id": requestid.Get(c)})
			return
		}
		if !ok {
			global.App.Log.WithFields(logrus.Fields{"request_id": requestid.Get(c), "username": username, "permission": permission}).Error("缺少权限")
			abortNoPermission(c, username, permission)
			return
		}
		c.Next()
	}
}

// RequireAnyPermission 满足其中任意一个权限点即放行。
//
// 用于「一个接口服务多种角色」的场景：数据访问申请列表既要给申请人看自己的，
// 也要给审批人看待办的，两者权限点不同，用单权限中间件只能二选一。
func RequireAnyPermission(permissions ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		username, ok := GetUserNameFromJWT(c)
		if !ok {
			c.AbortWithStatusJSON(401, gin.H{"code": 401, "msg": "认证信息无效", "data": nil, "request_id": requestid.Get(c)})
			return
		}
		for _, permission := range permissions {
			allowed, err := hasPermissionCode(username, permission)
			if err != nil {
				if errors.Is(err, services.ErrUserNotFound) {
					abortAuthInvalid(c, username)
					return
				}
				global.App.Log.WithFields(logrus.Fields{"request_id": requestid.Get(c), "username": username, "permission": permission, "error": err}).Error("权限校验失败")
				c.AbortWithStatusJSON(500, gin.H{"code": 500, "msg": "权限校验失败", "data": nil, "request_id": requestid.Get(c)})
				return
			}
			if allowed {
				c.Next()
				return
			}
		}
		global.App.Log.WithFields(logrus.Fields{"request_id": requestid.Get(c), "username": username, "permission": strings.Join(permissions, "|")}).Error("缺少权限")
		abortNoPermission(c, username, strings.Join(permissions, " | "))
	}
}

// UserHasAnyPermission 供视图层做数据范围判定（不中断请求）。
// 例如：只有持审批 / 全量查看权限的人才能看别人的申请单。
func UserHasAnyPermission(username string, permissions ...string) bool {
	for _, permission := range permissions {
		allowed, err := hasPermissionCode(username, permission)
		if err == nil && allowed {
			return true
		}
	}
	return false
}

// 下面方法暂时用不到，如果您有定制需求，可以取消注释，绑定到路由即可
// HasPermission 返回一个 gin.HandlerFunc，用于检查用户是否具有指定的权限
// 使用方法：v1.POST("generate-tasks", middleware.HasPermission("ExecuteOrders"), views.GenerateTasksView)
// func HasPermission(permission string) gin.HandlerFunc {
// 	return func(c *gin.Context) {
// 		// 从JWT声明中提取用户名
// 		username := jwt.ExtractClaims(c)["id"].(string)
// 		// 获取用户绑定的organization
// 		type organization struct {
// 			Key  string
// 			Path *datatypes.JSON // 此处使用指针支持返回空
// 		}
// 		var resultOrganization organization
// 		if err := global.App.DB.Select("io.key, io.path").
// 			Table("insight_org_users ou").
// 			Joins("join insight_users iu on ou.uid=iu.uid").
// 			Joins("join insight_orgs io on io.key=ou.organization_key").
// 			Where("iu.username=?", username).
// 			Scan(&resultOrganization).Error; err != nil {
// 			// 处理数据库查询错误
// 			global.App.Log.WithFields(logrus.Fields{"request_id": requestid.Get(c), "username": username, "error": err}).Error("无法获取用户的组织信息")
// 			c.AbortWithStatusJSON(500, gin.H{"code": 500, "msg": "无法获取用户的组织信息", "data": nil, "request_id": requestid.Get(c)})
// 			return
// 		}
// 		// 解析JSON数组
// 		var okeys []string
// 		if resultOrganization.Path != nil {
// 			if err := json.Unmarshal(*resultOrganization.Path, &okeys); err != nil {
// 				// 处理JSON解析错误
// 				global.App.Log.WithFields(logrus.Fields{"request_id": requestid.Get(c), "username": username, "error": err}).Error("无法解析用户的组织信息")
// 				c.AbortWithStatusJSON(500, gin.H{"code": 500, "msg": "无法解析用户的组织信息", "data": nil, "request_id": requestid.Get(c)})
// 				return
// 			}
// 		}

// 		okeys = append(okeys, resultOrganization.Key)
// 		// 查看用户的权限
// 		type count struct {
// 			Count int
// 		}
// 		var resultCount count
// 		if err := global.App.DB.Select("count(*) as count").
// 			Table("insight_permissions_organizations a").
// 			Joins("join insight_permissions b on a.permission_id=b.id").
// 			Joins("join insight_orgs c on a.organization_key=c.key").
// 			Where("c.key in ? and b.name=?", okeys, permission).Scan(&resultCount).Error; err != nil {
// 			// 处理数据库查询错误
// 			global.App.Log.WithFields(logrus.Fields{"request_id": requestid.Get(c), "username": username, "error": err}).Error("无法查询用户的权限信息")
// 			c.AbortWithStatusJSON(500, gin.H{"code": 500, "msg": "无法查询用户的权限信息", "data": nil, "request_id": requestid.Get(c)})
// 			return
// 		}

// 		if resultCount.Count == 0 {
// 			global.App.Log.WithFields(logrus.Fields{"request_id": requestid.Get(c), "username": username}).Errorf("您没有%s权限", permission)
// 			c.AbortWithStatusJSON(403, gin.H{"code": 403, "msg": fmt.Sprintf("您没有%s权限", permission), "data": nil, "request_id": requestid.Get(c)})
// 		}
// 		// 用户有权限，调用链中的下一个处理程序
// 		c.Next()
// 	}
// }
