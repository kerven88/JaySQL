package bootstrap

import (
	"github.com/simajiong/jaysql/middleware"

	"github.com/simajiong/jaysql/internal/global"
)

func InitializeLog() {
	global.App.Log = middleware.InitLogger("app.log")
}
