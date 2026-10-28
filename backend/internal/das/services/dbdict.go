package services

import (
	"errors"
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/internal/das/dao"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/parser"
	"github.com/simajiong/jaysql/pkg/secret"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type GetDbDictService struct {
	*forms.GetDbDictForm
	C        *gin.Context
	Username string
}

func (s *GetDbDictService) parserUUID() (id string, err error) {
	id = strings.TrimSpace(s.InstanceID)
	if id == "" {
		return "", fmt.Errorf("instance id is empty")
	}
	return id, nil
}

// 验证用户是否有指定schema的权限
func (s *GetDbDictService) validatePerms(uuid string) error {
	// 检查库表权限
	var tables []parser.Table
	tables = append(tables, parser.Table{Schema: s.Schema})
	checker := CheckUserPerm{
		UserName:   s.Username,
		InstanceID: uuid,
		Tables:     tables,
	}
	if err := checker.HasSchemaPerms(); err != nil {
		return err
	}
	return nil
}

func (s *GetDbDictService) getInstanceCfg() (record models.InsightInstances, err error) {
	// 获取DB配置：额外带上 db_type / extra_config —— 家族判定（尤其 OceanBase 的租户模式）
	// 需要它们，一次查询拿全，避免再单独查一次 db_type。
	r := global.App.DB.Table("`insight_instances` a").
		Select("a.`hostname`, a.`port`, a.`user`, a.`password`, a.`db_type`, a.`extra_config`").
		Where("a.instance_id=?", s.InstanceID).
		Take(&record)
	// 判断记录是否存在
	if errors.Is(r.Error, gorm.ErrRecordNotFound) {
		return record, fmt.Errorf("指定DB配置的记录不存在,错误的信息:%s", r.Error.Error())
	}
	return record, nil
}

func (s *GetDbDictService) getDbDict(instanceCfg models.InsightInstances) (data *[]map[string]interface{}, err error) {
	if err := validateIdentifier(s.Schema, "schema"); err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
					select
						t.TABLE_NAME,
						t.TABLE_COMMENT,
						t.CREATE_TIME,
						group_concat(
							distinct concat_ws(
								'<b>',
								c.COLUMN_NAME,
								c.COLUMN_TYPE,
								if(c.IS_NULLABLE = 'NO', 'NOT NULL', 'NULL'),
								ifnull(c.COLUMN_DEFAULT, ''),
								ifnull(c.CHARACTER_SET_NAME, ''),
								ifnull(c.COLLATION_NAME, ''),
								ifnull(c.COLUMN_COMMENT, '')
							) separator '<a>'
						) as COLUMNS_INFO,
						group_concat(
							distinct concat_ws(
								'<b>',
								s.INDEX_NAME,
								if(s.NON_UNIQUE = 0, '唯一', '不唯一'),
								s.Cardinality,
								s.INDEX_TYPE,
								s.COLUMN_NAME
							) separator '<a>'
						) as INDEXES_INFO
					from
						COLUMNS c
						join TABLES t on c.TABLE_SCHEMA = t.TABLE_SCHEMA
						and c.TABLE_NAME = t.TABLE_NAME
						left join STATISTICS s on c.TABLE_SCHEMA = s.TABLE_SCHEMA
						and c.TABLE_NAME = s.TABLE_NAME
						where
							t.TABLE_SCHEMA = %s
						group by
							t.TABLE_NAME,
							t.TABLE_COMMENT,
							t.CREATE_TIME
					`, quoteStringLiteral(s.Schema))

	// 解密密码
	plainPassword, err := secret.Decrypt(instanceCfg.Password)
	if err != nil {
		return nil, err
	}

	db := dao.DB{
		User:     instanceCfg.User,
		Password: plainPassword,
		Host:     instanceCfg.Hostname,
		Port:     instanceCfg.Port,
		Database: "information_schema",
		Params:   map[string]string{"group_concat_max_len": "1073741824"},
		Ctx:      s.C.Request.Context(),
	}

	_, data, err = db.Query(query)
	if err != nil {
		global.App.Log.Error(err.Error())
	}
	return data, err
}

func (s *GetDbDictService) Run() (responseData *[]map[string]interface{}, err error) {
	// 解析UUID
	uuid, err := s.parserUUID()
	if err != nil {
		return responseData, err
	}
	// 验证用户是否有指定schema的权限
	if err = s.validatePerms(uuid); err != nil {
		return responseData, err
	}
	// 获取DB配置
	instanceCfg, err := s.getInstanceCfg()
	if err != nil {
		return responseData, err
	}

	// 家族判定：OceanBase 的 MySQL 模式复用 MySQL 数据字典实现。
	dbType := string(instanceCfg.DbType)
	if IsMySQLFamilyCfg(dbType, instanceCfg.ExtraConfig) {
		return s.getDbDict(instanceCfg)
	}
	return responseData, fmt.Errorf("%s不支持获取数据字典", dbType)
}
