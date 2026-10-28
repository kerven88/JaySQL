package routers

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
)

func Routers(r *gin.Engine) {
	adminV1 := r.Group("/api/v1/admin/inspect")
	adminV1.Use(global.App.JWT.MiddlewareFunc())
	{
		RegisterAdminRoutes(adminV1)
	}
}
