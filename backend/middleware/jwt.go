package middleware

import (
	"time"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/users/services"
	"github.com/simajiong/jaysql/pkg/response"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/gin-gonic/gin"
)

var identityKey = "id"

// payload
type User struct {
	UserName string
}

func InitAuthMiddleware() (*jwt.GinJWTMiddleware, error) {
	return jwt.New(&jwt.GinJWTMiddleware{
		Realm:       "JaySQL",
		Key:         []byte(global.App.Config.App.SECRET_KEY),
		Timeout:     24 * time.Hour,
		MaxRefresh:  24 * time.Hour,
		IdentityKey: identityKey,
		LoginResponse: func(c *gin.Context, code int, token string, expire time.Time) {
			requestID := c.GetString("X-Request-ID")
			c.JSON(code, response.Response{
				RequestID: requestID,
				Code:      "0000",
				Data:      gin.H{"token": token, "expire": expire.Format(time.RFC3339)},
				Message:   "登录成功",
			})
		},
		PayloadFunc: func(data interface{}) jwt.MapClaims {
			if v, ok := data.(*User); ok {
				return jwt.MapClaims{
					identityKey: v.UserName,
				}
			}
			return jwt.MapClaims{}
		},
		IdentityHandler: func(c *gin.Context) interface{} {
			username, ok := GetUserNameFromJWT(c)
			if !ok {
				return &User{}
			}
			return &User{
				UserName: username,
			}
		},
		Authenticator: func(c *gin.Context) (interface{}, error) {
			// login username from LoginCheckMiddleware
			username := c.GetString("loginUserName")
			if len(username) == 0 {
				return nil, jwt.ErrMissingLoginValues
			}
			// 与登录校验（LoginCheckMiddleware）共用同一套「同名多行」判定口径，
			// 并按 uid 回写 last_login —— 按 username 更新会连已软删除的同名历史行一起刷，
			// 使得"旧账号今天登录过"这种假象出现在排查现场。
			user, err := services.CanonicalUserByUsername(username)
			if err != nil {
				return nil, jwt.ErrFailedAuthentication
			}
			global.App.DB.Table("insight_users u").
				Where("u.uid = ?", user.Uid).
				Update("last_login", time.Now().Format("2006-01-02 15:04:05"))
			// payload
			return &User{
				UserName: user.Username,
			}, nil
		},
		Unauthorized: func(c *gin.Context, code int, message string) {
			c.JSON(code, gin.H{
				"code":    code,
				"message": message,
			})
		},
		// TokenLookup is a string in the form of "<source>:<name>" that is used
		// to extract token from the request.
		// Optional. Default value "header:Authorization".
		// Possible values:
		// - "header:<name>"
		// - "query:<name>"
		// - "cookie:<name>"
		// - "param:<name>"
		TokenLookup: "header: Authorization, cookie: jwt",
		// TokenLookup: "query:token",
		// TokenLookup: "cookie:token",

		// TokenHeadName is a string in the header. Default value is "Bearer"
		TokenHeadName: "JWT",

		// TimeFunc provides the current time. You can override it to use another time value. This is useful for testing or if your server uses a different time zone than your tokens.
		TimeFunc: time.Now,
	})

}
