package views

// ============================================================
// 品牌设置 —— 视图层
//
// 公开侧：GET /api/v1/brand（不鉴权，登录页未登录也要能拉到 Logo）
// 管理侧：POST   /api/v1/admin/brand/logo       上传 / 替换
//         DELETE /api/v1/admin/brand/logo?type=  恢复内置默认
// ============================================================

import (
	"strings"

	brandForms "github.com/simajiong/jaysql/internal/brand/forms"
	brandServices "github.com/simajiong/jaysql/internal/brand/services"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/middleware"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// GetBrandView 读取品牌配置（公开接口）
//
// 失败时按"空配置"返回而不是报错：品牌配置读不出来最多是不显示自定义 Logo，
// 不该把登录页整个搞挂。
func GetBrandView(c *gin.Context) {
	cfg, err := (&brandServices.GetBrandService{}).Run()
	if err != nil {
		global.App.Log.WithFields(logrus.Fields{
			"request_id": requestid.Get(c),
			"error":      err,
		}).Warn("读取品牌配置失败，回落内置默认 Logo")
		response.Success(c, &brandServices.BrandConfig{}, "success")
		return
	}
	response.Success(c, cfg, "success")
}

// UploadBrandLogoView 上传 / 替换 Logo（管理员）
func UploadBrandLogoView(c *gin.Context) {
	var form brandForms.BrandLogoTypeForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	logoType := strings.TrimSpace(form.Type)
	if logoType == "" {
		// multipart 里字段顺序不保证，显式补两次：body 表单 → URL query
		logoType = strings.TrimSpace(c.PostForm("type"))
	}
	if logoType == "" {
		logoType = strings.TrimSpace(c.Query("type"))
	}
	if logoType == "" {
		response.ValidateFail(c, "缺少 type 参数（login / sidebar）")
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		response.ValidateFail(c, "缺少 file 参数")
		return
	}

	cfg, err := (&brandServices.UploadBrandLogoService{C: c, Type: logoType, File: file}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	global.App.Log.WithFields(logrus.Fields{
		"request_id": requestid.Get(c),
		"username":   currentBrandOperator(c),
		"type":       logoType,
	}).Info("更新品牌 Logo")
	response.Success(c, cfg, "success")
}

// ResetBrandLogoView 恢复内置默认 Logo（管理员）
func ResetBrandLogoView(c *gin.Context) {
	logoType := strings.TrimSpace(c.Query("type"))
	if logoType == "" {
		response.ValidateFail(c, "缺少 type 参数（login / sidebar）")
		return
	}
	cfg, err := (&brandServices.ResetBrandLogoService{Type: logoType}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	global.App.Log.WithFields(logrus.Fields{
		"request_id": requestid.Get(c),
		"username":   currentBrandOperator(c),
		"type":       logoType,
	}).Info("恢复内置默认 Logo")
	response.Success(c, cfg, "success")
}

// UpdateBrandTextView 保存品牌文字（管理员）
//
// 注意：这里只做参数接收，长度 / 控制字符校验统一在 services.normalizeBrandText，
// 保证读取（公开接口）与写入走同一套净化规则，不会出现"写进去一个值、读出来变样"。
func UpdateBrandTextView(c *gin.Context) {
	var form brandForms.BrandTextForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	cfg, err := (&brandServices.UpdateBrandTextService{
		BrandName: form.BrandName,
		HomeTitle: form.HomeTitle,
	}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	global.App.Log.WithFields(logrus.Fields{
		"request_id": requestid.Get(c),
		"username":   currentBrandOperator(c),
	}).Info("更新品牌文字")
	response.Success(c, cfg, "success")
}

// currentBrandOperator 取操作人用户名，仅用于审计日志；取不到不影响主流程。
func currentBrandOperator(c *gin.Context) string {
	username, ok := middleware.GetUserNameFromJWT(c)
	if !ok {
		return ""
	}
	return username
}
