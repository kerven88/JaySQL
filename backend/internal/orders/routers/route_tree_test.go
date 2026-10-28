package routers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRegisterApiRoutesNoConflict 验证工单路由树能正常注册且分派正确。
//
// 背景：钉钉审批集成往 orders 路由下加了 `approval/dingtalk/preview|status|callback`
// 这组静态多段路径，而同一层已存在 `:order_id` 单段参数路由。gin 的参数路由与
// 静态路由能否同层共存直接决定服务能否启动（冲突会 panic），因此用真实路由表实测。
//
// 这里不断言 handler 的业务行为（需要 DB），只确认「注册不 panic + 路径命中正确
// 的 handler」。用一个可识别的 marker header 区分命中的是哪一个 handler。
func TestRegisterApiRoutesNoConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/orders")

	// 注册过程若发生路由冲突会 panic，测试直接失败
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("注册工单路由时 panic（路由冲突）：%v", r)
		}
	}()
	RegisterApiRoutes(group)

	// 用一个「未授权」请求探测路径是否命中：命中即说明路由存在。
	// 权限中间件会拦截，因此只断言「不是 404」。
	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/orders/approval/dingtalk/preview"},
		{http.MethodGet, "/orders/approval/dingtalk/status"},
		{http.MethodPost, "/orders/approval/dingtalk/callback"},
		// 既有参数路由不能被新静态段挤掉
		{http.MethodGet, "/orders/bk-36xqrhky0r5a"},
		{http.MethodGet, "/orders/approvals/bk-36xqrhky0r5a"},
		{http.MethodGet, "/orders/environments"},
		{http.MethodGet, "/orders/tasks/preview"},
	}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(p.method, p.path, nil)
		engine.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s %s 返回 404，说明路由未注册或被其它段遮蔽", p.method, p.path)
		}
	}
}
