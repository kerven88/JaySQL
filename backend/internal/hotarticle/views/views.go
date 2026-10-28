package views

// ============================================================
// 首页相关热文 —— 视图层
//
// 管理侧：/api/v1/admin/hot-articles（system.hotarticle.* 权限）
// 用户侧：/api/v1/home/hot-articles（仅要求登录，首页全员可见）
// ============================================================

import (
	"strconv"
	"strings"

	hotForms "github.com/simajiong/jaysql/internal/hotarticle/forms"
	hotServices "github.com/simajiong/jaysql/internal/hotarticle/services"
	"github.com/simajiong/jaysql/middleware"
	"github.com/simajiong/jaysql/pkg/response"

	"github.com/gin-gonic/gin"
)

// ListHotArticlesView 热文列表（管理员，分页）
func ListHotArticlesView(c *gin.Context) {
	var form hotForms.HotArticleListQueryForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	rows, total, err := (&hotServices.ListHotArticlesService{Form: &form}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.PaginationSuccess(c, total, rows)
}

// CreateHotArticleView 新增热文
func CreateHotArticleView(c *gin.Context) {
	var form hotForms.HotArticleCreateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	operator, _ := middleware.GetUserNameFromJWT(c)
	record, err := (&hotServices.CreateHotArticleService{Form: &form, Operator: operator}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "新增成功")
}

// UpdateHotArticleView 编辑热文
func UpdateHotArticleView(c *gin.Context) {
	id, ok := parseHotArticleID(c)
	if !ok {
		return
	}
	var form hotForms.HotArticleUpdateForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	operator, _ := middleware.GetUserNameFromJWT(c)
	record, err := (&hotServices.UpdateHotArticleService{ID: id, Form: &form, Operator: operator}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "已保存")
}

// UpdateHotArticleStatusView 启用 / 停用
func UpdateHotArticleStatusView(c *gin.Context) {
	id, ok := parseHotArticleID(c)
	if !ok {
		return
	}
	var form hotForms.HotArticleStatusForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	operator, _ := middleware.GetUserNameFromJWT(c)
	record, err := (&hotServices.UpdateHotArticleStatusService{
		ID:       id,
		Enabled:  form.Enabled,
		Operator: operator,
	}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, record, "已更新")
}

// UpdateHotArticleSortView 批量排序（列表页上移/下移）
func UpdateHotArticleSortView(c *gin.Context) {
	var form hotForms.HotArticleSortForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	if err := (&hotServices.UpdateHotArticleSortService{Items: form.Items}).Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "排序已保存")
}

// DeleteHotArticleView 删除热文
func DeleteHotArticleView(c *gin.Context) {
	id, ok := parseHotArticleID(c)
	if !ok {
		return
	}
	if err := (&hotServices.DeleteHotArticleService{ID: id}).Run(); err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, nil, "已删除")
}

// HomeHotArticlesView 首页「相关热文」数据源：已启用且排序前 N 条。
// 仅要求登录（首页是登录后第一屏，任何用户都要能看到）。数据为空是合法结果，
// 由前端渲染空态；前端在接口异常时会回退内置静态内容，保证首页不空。
func HomeHotArticlesView(c *gin.Context) {
	items, err := (&hotServices.HomeHotArticlesService{}).Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"articles": items}, "success")
}

func parseHotArticleID(c *gin.Context) (uint64, bool) {
	raw := strings.TrimSpace(c.Param("id"))
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		response.ValidateFail(c, "非法参数: id")
		return 0, false
	}
	return id, true
}
