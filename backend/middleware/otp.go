package middleware

import (
	"errors"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/users/services"

	userModels "github.com/simajiong/jaysql/internal/users/models"

	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-contrib/requestid"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

type login struct {
	Username string `form:"username" json:"username" binding:"required"`
	Password string `form:"password" json:"password" binding:"required"`
}

type loginUserInfo struct {
	username string
	password string
	user     userModels.InsightUsers
}

func (u *loginUserInfo) checkIfUserExist() (err error) {
	// 用户唯一性只由 uid 决定后，同名用户可能存在多条（含软删除/未激活的历史记录）。
	// 登录必须确定性地选中"正常"那条：优先未删除 → 已激活 → uid 最小（最早创建）。
	// 该口径收敛在 users/services.ResolveCanonicalUser 内，权限解析（RequirePermission /
	// 用户信息接口）与登录共用同一份实现，避免"登录用一个 uid、鉴权用另一个 uid"的错位。
	user, err := services.CanonicalUserByUsername(u.username)
	if err != nil {
		return errors.New("用户名或密码错误")
	}
	u.user = user
	return nil
}

func (u *loginUserInfo) checkUserValid() error {
	if !u.user.IsActive {
		return errors.New("用户未激活")
	}
	err := bcrypt.CompareHashAndPassword([]byte(u.user.Password), []byte(u.password))
	if err != nil {
		return errors.New("用户名或密码错误")
	}
	return nil
}

func LoginCheckMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var loginVals login
		if err := c.ShouldBind(&loginVals); err != nil {
			response.Fail(c, "missing Username or Password")
			c.Abort()
			return
		}
		username := loginVals.Username
		password := loginVals.Password

		check := loginUserInfo{username: username, password: password}
		if err := check.checkIfUserExist(); err != nil {
			global.App.Log.WithFields(logrus.Fields{"request_id": requestid.Get(c), "username": username, "error": err}).Error(err.Error())
			c.AbortWithStatusJSON(400, gin.H{"message": err.Error(), "data": nil, "request_id": requestid.Get(c)})
			return
		}
		if err := check.checkUserValid(); err != nil {
			global.App.Log.WithFields(logrus.Fields{"request_id": requestid.Get(c), "username": username, "error": err}).Error(err.Error())
			c.AbortWithStatusJSON(400, gin.H{"message": err.Error(), "data": nil, "request_id": requestid.Get(c)})
			return
		}

		// 写入 JWT 的用户名取「解析出的那一行」的用户名，保证后续按 username 的
		// 权限校验/业务归属都落回同一行记录（同名多行场景下不会再指向历史行）。
		c.Set("loginUserName", check.user.Username)

		c.Next()
	}
}
