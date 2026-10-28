package services

// ============================================================
// 品牌设置 —— 业务层
//
// 存储选型：复用通用 KV 表 insight_system_params（common/models/sysparams.go）。
// 理由：这张表已在 bootstrap 的 AutoMigrate 列表里、config_key 带唯一索引，
// 用它可以做到"本次改动零 DDL"——发布无需执行迁移脚本，回滚只需回退二进制。
//
// 两个 key：
//   brand_login_logo_url   登录页大 Logo
//   brand_sidebar_logo_url 页面左上角 Logo
//
// 值一律存**相对 URL**（形如 /media/brand/login_1730000000.png），
// 由 r.Static("/media", "./media") 无鉴权直出，登录页未登录态也能加载。
// ============================================================

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	// LoginLogoConfigKey 登录页 Logo 的系统参数键
	LoginLogoConfigKey = "brand_login_logo_url"
	// SidebarLogoConfigKey 侧栏左上角 Logo 的系统参数键
	SidebarLogoConfigKey = "brand_sidebar_logo_url"
	// BrandNameConfigKey 品牌名称（侧栏左上角 Logo 旁的文字）的系统参数键
	BrandNameConfigKey = "brand_name"
	// HomeTitleConfigKey 首页大标题（如「JaySQL DevOps」）的系统参数键
	HomeTitleConfigKey = "brand_home_title"

	// LogoTypeLogin / LogoTypeSidebar 上传接口 type 字段的合法取值
	LogoTypeLogin   = "login"
	LogoTypeSidebar = "sidebar"

	// brandDir 落盘目录（运行目录下的 ./media/brand，不在 go:embed 包里，升级不丢）
	brandDir = "./media/brand"
	// brandURLPrefix 对外 URL 前缀，与 internal/app/app.go 的 r.Static("/media", "./media") 对应
	brandURLPrefix = "/media/brand/"

	// maxLogoBytes 单张 Logo 大小上限 2MB
	maxLogoBytes = 2 << 20

	// maxBrandTextLen 品牌文字（名称 / 首页标题）长度上限。
	// 侧栏文字过长会把折叠态顶栏挤爆，32 字符足够放正常品牌名。
	maxBrandTextLen = 32

	// BrandLogoDesc 写入系统参数表时的描述文案
	BrandLogoDesc = "品牌设置：自定义 Logo 的静态文件地址"
	// BrandTextDesc 写入系统参数表时的描述文案
	BrandTextDesc = "品牌设置：自定义品牌文字（空串表示使用内置默认）"
)

// BrandConfig 对外下发的品牌配置。
// Logo 两个字段为空串表示"用内置默认图"；文字两个字段为空串表示"用内置默认文案"。
type BrandConfig struct {
	LoginLogoURL   string `json:"login_logo_url"`
	SidebarLogoURL string `json:"sidebar_logo_url"`
	BrandName      string `json:"brand_name"`
	HomeTitle      string `json:"home_title"`
}

// configKeyOf 把上传接口的 type 映射到系统参数键。
func configKeyOf(logoType string) (string, bool) {
	switch strings.TrimSpace(logoType) {
	case LogoTypeLogin:
		return LoginLogoConfigKey, true
	case LogoTypeSidebar:
		return SidebarLogoConfigKey, true
	}
	return "", false
}

// readRawParam 读取一个品牌参数的原始值，缺失返回空串（不抛错：没配过就用默认）。
func readRawParam(configKey string) (string, error) {
	var row commonModels.InsightSystemParam
	if err := global.App.DB.
		Select("config_value").
		Where("config_key = ?", configKey).
		Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return row.ConfigValue, nil
}

// readBrandURL 读取一个 Logo 参数并按 URL 规则净化。
func readBrandURL(configKey string) (string, error) {
	raw, err := readRawParam(configKey)
	if err != nil {
		return "", err
	}
	return sanitizeBrandURL(raw), nil
}

// writeBrandParam 写入一个品牌参数（先查后写，通用 KV 表行数极少，不上 upsert）。
func writeBrandParam(configKey, value string) error {
	var row commonModels.InsightSystemParam
	err := global.App.DB.Where("config_key = ?", configKey).Take(&row).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row = commonModels.InsightSystemParam{
			Title:       brandTitleOf(configKey),
			ConfigKey:   configKey,
			ConfigValue: value,
			ConfigType:  "string",
			Description: brandDescOf(configKey),
		}
		return global.App.DB.Create(&row).Error
	}
	return global.App.DB.Model(&row).
		Updates(map[string]any{"config_value": value, "title": brandTitleOf(configKey)}).Error
}

func brandTitleOf(configKey string) string {
	switch configKey {
	case LoginLogoConfigKey:
		return "登录页 Logo"
	case SidebarLogoConfigKey:
		return "侧栏左上角 Logo"
	case BrandNameConfigKey:
		return "品牌名称"
	case HomeTitleConfigKey:
		return "首页大标题"
	}
	return configKey
}

func brandDescOf(configKey string) string {
	if configKey == BrandNameConfigKey || configKey == HomeTitleConfigKey {
		return BrandTextDesc
	}
	return BrandLogoDesc
}

// sanitizeBrandURL 净化 Logo 地址：只认本站 /media/brand/ 下的相对路径。
//
// 为什么必须净化：这个值最终会被前端直接塞进 <img src>。库里若被写进外链或
// javascript: 之类的内容，就会变成一个存储型 XSS / 外链追踪点。宁可回落默认图。
func sanitizeBrandURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, brandURLPrefix) {
		return ""
	}
	// 拒绝 ../ 穿越与多级路径
	name := strings.TrimPrefix(value, brandURLPrefix)
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return ""
	}
	return value
}

// normalizeBrandText 净化品牌文字（名称 / 首页标题）。
//
// 前端是 Vue 插值渲染（自动转义），没有 XSS 面，但要防两件事：
// 1) 换行 / 制表符等控制字符把顶栏布局撑乱——压成单个空格；
// 2) 超长文字挤爆侧栏——超限直接判空回落默认文案（而不是截断出半截词）。
func normalizeBrandText(raw string, maxLen int) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r == 0x0B || r == 0x0C {
			return ' '
		}
		if r < 0x20 {
			return -1 // 其余控制字符直接剔除
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > maxLen {
		return ""
	}
	return value
}

// ---------- 读取（公开） ----------

// GetBrandService 读取品牌配置
type GetBrandService struct{}

// Run 返回当前品牌配置。任一键缺失即为空串，前端回落内置默认图 / 默认文案。
func (s *GetBrandService) Run() (*BrandConfig, error) {
	loginURL, err := readBrandURL(LoginLogoConfigKey)
	if err != nil {
		return nil, err
	}
	sidebarURL, err := readBrandURL(SidebarLogoConfigKey)
	if err != nil {
		return nil, err
	}
	brandName, err := readRawParam(BrandNameConfigKey)
	if err != nil {
		return nil, err
	}
	homeTitle, err := readRawParam(HomeTitleConfigKey)
	if err != nil {
		return nil, err
	}
	return &BrandConfig{
		LoginLogoURL:   loginURL,
		SidebarLogoURL: sidebarURL,
		BrandName:      normalizeBrandText(brandName, maxBrandTextLen),
		HomeTitle:      normalizeBrandText(homeTitle, maxBrandTextLen),
	}, nil
}

// ---------- 品牌文字（管理员） ----------

// UpdateBrandTextService 保存品牌名称 / 首页大标题
type UpdateBrandTextService struct {
	BrandName string
	HomeTitle string
}

// Run 两个文字字段一起保存（页面上是一个卡片一个保存按钮，语义一致）。
// 空串 = 恢复内置默认文案。
func (s *UpdateBrandTextService) Run() (*BrandConfig, error) {
	brandName := normalizeBrandText(s.BrandName, maxBrandTextLen)
	homeTitle := normalizeBrandText(s.HomeTitle, maxBrandTextLen)
	if err := writeBrandParam(BrandNameConfigKey, brandName); err != nil {
		return nil, err
	}
	if err := writeBrandParam(HomeTitleConfigKey, homeTitle); err != nil {
		return nil, err
	}
	return (&GetBrandService{}).Run()
}

// ---------- 上传（管理员） ----------

// UploadBrandLogoService 上传并替换某个位置的 Logo
type UploadBrandLogoService struct {
	C    *gin.Context
	Type string
	File *multipart.FileHeader
}

// Run 校验 → 落盘 → 写库 → 清理旧文件 → 回读最新配置。
func (s *UploadBrandLogoService) Run() (*BrandConfig, error) {
	configKey, ok := configKeyOf(s.Type)
	if !ok {
		return nil, fmt.Errorf("Logo 位置非法，仅支持 login / sidebar")
	}
	if s.File == nil {
		return nil, fmt.Errorf("未接收到文件")
	}
	if s.File.Size > maxLogoBytes {
		return nil, fmt.Errorf("图片超过 2MB，请压缩后重试")
	}

	ext, err := sniffImageExt(s.File)
	if err != nil {
		return nil, err
	}

	// 文件名带时间戳：既避免覆盖，也天然绕开浏览器缓存（换图即时生效）
	fileName := fmt.Sprintf("%s_%d.%s", s.Type, time.Now().Unix(), ext)
	dst := filepath.Join(brandDir, fileName)
	if err := s.C.SaveUploadedFile(s.File, dst); err != nil {
		return nil, fmt.Errorf("保存文件失败: %v", err)
	}

	// 先拿到旧地址，写库成功后再删旧文件——写库失败时不留"库里是旧的、磁盘上旧文件已删"的坑
	oldURL, err := readBrandURL(configKey)
	if err != nil {
		_ = os.Remove(dst)
		return nil, err
	}
	nextURL := brandURLPrefix + fileName
	if err := writeBrandParam(configKey, nextURL); err != nil {
		_ = os.Remove(dst)
		return nil, err
	}
	removeOldBrandFile(oldURL, fileName)

	return (&GetBrandService{}).Run()
}

// ---------- 恢复默认（管理员） ----------

// ResetBrandLogoService 清空某个位置的自定义 Logo，回落内置默认图
type ResetBrandLogoService struct {
	Type string
}

// Run 只清配置值，不删磁盘文件——删除是不可逆动作，而"恢复默认"语义上
// 只是不引用它。残留文件最多几 KB~几 MB，留给运维按需清理更安全。
func (s *ResetBrandLogoService) Run() (*BrandConfig, error) {
	configKey, ok := configKeyOf(s.Type)
	if !ok {
		return nil, fmt.Errorf("Logo 位置非法，仅支持 login / sidebar")
	}
	if err := writeBrandParam(configKey, ""); err != nil {
		return nil, err
	}
	return (&GetBrandService{}).Run()
}

// ---------- 内部工具 ----------

// sniffImageExt 按**文件头魔数**判定图片类型并返回扩展名。
//
// 为什么不看扩展名 / 不看 Content-Type：这两者都由客户端提供，改一个字节就能骗过。
// 只收 png / jpg / webp，明确不收 svg——/media 是静态直出，svg 会带 image/svg+xml
// 响应头，用户直接访问该 URL 时脚本可执行，属于存储型 XSS 面；三种位图足够覆盖诉求。
func sniffImageExt(file *multipart.FileHeader) (string, error) {
	src, err := file.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()
	return sniffImageExtFromReader(src)
}

// sniffImageExtFromReader 真正的判定逻辑，单独抽出以便单测覆盖（*multipart.FileHeader
// 依赖磁盘临时文件，测试里构造成本高，而 io.Reader 随手就能造）。
func sniffImageExtFromReader(src io.Reader) (string, error) {
	buf := make([]byte, 512)
	n, err := io.ReadFull(src, buf)
	// 小图片不足 512 字节属正常，只要读到了内容就继续判定
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	buf = buf[:n]
	if len(buf) < 12 {
		return "", fmt.Errorf("文件内容不完整，无法识别图片格式")
	}

	switch {
	case string(buf[0:8]) == "\x89PNG\r\n\x1a\n":
		return "png", nil
	case buf[0] == 0xFF && buf[1] == 0xD8 && buf[2] == 0xFF:
		return "jpg", nil
	case string(buf[0:4]) == "RIFF" && string(buf[8:12]) == "WEBP":
		return "webp", nil
	}
	return "", fmt.Errorf("不支持的图片格式，仅支持 png / jpg / webp")
}

// removeOldBrandFile 删除被替换掉的旧 Logo 文件。
//
// 三重把关，确保这个函数永远只能删 ./media/brand/ 下的直接文件：
// 1) URL 必须带 /media/brand/ 前缀；2) 文件名不含路径分隔符；3) 不含 ..
func removeOldBrandFile(oldURL string, keepName string) {
	if !strings.HasPrefix(oldURL, brandURLPrefix) {
		return
	}
	name := strings.TrimPrefix(oldURL, brandURLPrefix)
	if name == "" || name == keepName {
		return
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return
	}
	_ = os.Remove(filepath.Join(brandDir, name))
}
