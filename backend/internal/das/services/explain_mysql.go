package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/models"
	"github.com/simajiong/jaysql/internal/das/parser"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/query"
	"github.com/simajiong/jaysql/pkg/utils"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"gorm.io/datatypes"
)

type ExplainMySQLQueryService struct {
	*forms.ExecuteMySQLQueryForm
	C        *gin.Context
	Username string
}

func (s *ExplainMySQLQueryService) validateStatementSyntax() (*parser.TiStmt, error) {
	TiStmt, warns, err := parser.NewParse(s.SQLText, "", "")
	if len(warns) > 0 {
		return TiStmt, fmt.Errorf("Parse Warning: %s", utils.ErrsJoin("; ", warns))
	}
	if err != nil {
		return TiStmt, fmt.Errorf("SQL语法解析错误:%s", err.Error())
	}
	return TiStmt, nil
}

func (s *ExplainMySQLQueryService) validateStatementNum(stmts []ast.StmtNode) error {
	if len(stmts) > 1 {
		return fmt.Errorf("解释功能每次仅支持一条语句，当前语句数量:%d", len(stmts))
	}
	return nil
}

func (s *ExplainMySQLQueryService) validateStatementType(stmt ast.StmtNode) error {
	st := parser.StatementType{}
	statementType := st.Extract(stmt)
	if statementType != "SELECT" && statementType != "UNION" {
		return fmt.Errorf("解释功能当前仅支持单条 SELECT / WITH 查询")
	}
	return nil
}

func (s *ExplainMySQLQueryService) Run() (ResponseData, error) {
	var responseData ResponseData = ResponseData{SQLText: s.SQLText}
	params, err := json.Marshal(s.Params)
	if err != nil {
		return responseData, err
	}

	record := &models.InsightDASRecords{
		Username:  s.Username,
		Schema:    s.Schema,
		Sqltext:   s.SQLText,
		RequestID: requestid.Get(s.C),
		Params:    datatypes.JSON(params),
	}
	global.App.DB.Create(&record)

	uuid, err := ParserUUID(s.InstanceID)
	if err != nil {
		return responseData, err
	}
	global.App.DB.Model(&models.InsightDASRecords{}).
		Where("request_id=? and username=?", requestid.Get(s.C), s.Username).
		Update("InstanceID", uuid)

	dbType, err := GetDbType(s.InstanceID)
	if err != nil {
		return responseData, err
	}
	if !IsMySQLFamily(dbType) {
		return responseData, fmt.Errorf("解释功能当前仅支持 MySQL 系数据库，当前DB类型为%s", dbType)
	}

	instanceCfg, err := GetDBConfig(s.InstanceID)
	if err != nil {
		return responseData, err
	}

	TiStmt, err := s.validateStatementSyntax()
	if err != nil {
		return responseData, err
	}
	if err := s.validateStatementNum(TiStmt.Stmts); err != nil {
		return responseData, err
	}

	singleStmt := TiStmt.Stmts[0]
	if err := s.validateStatementType(singleStmt); err != nil {
		return responseData, err
	}

	fingerId := query.Id(query.Fingerprint(singleStmt.Text()))
	responseData.QueryID = fingerId
	global.App.DB.Model(&models.InsightDASRecords{}).
		Where("request_id=? and username=?", requestid.Get(s.C), s.Username).
		Update("QueryID", fingerId)

	extracter := parser.Extracter{Stmt: singleStmt, Schema: s.Schema}
	extractTables := extracter.Run()
	var tmpExtractTables []string
	for _, tt := range extractTables {
		tmpExtractTables = append(tmpExtractTables, strings.Join([]string{tt.Schema, tt.Table}, "."))
	}
	global.App.DB.Model(&models.InsightDASRecords{}).
		Where("request_id=? and username=?", requestid.Get(s.C), s.Username).
		Update("Tables", strings.Join(tmpExtractTables, ";"))

	checker := CheckUserPerm{
		UserName:   s.Username,
		InstanceID: uuid,
		Tables:     extractTables,
	}
	if err := checker.HasSchemaPerms(); err != nil {
		return responseData, err
	}
	if err := checker.HasTablePerms(); err != nil {
		return responseData, err
	}

	s.SQLText = fmt.Sprintf("EXPLAIN %s", strings.TrimSpace(singleStmt.Text()))
	responseData.SQLText = s.SQLText

	global.App.DB.Model(&models.InsightDASRecords{}).
		Where("request_id=? and username=?", requestid.Get(s.C), s.Username).
		Update("RewriteSqltext", s.SQLText)

	executeApi := MySQLExecuteApi{ExecuteMySQLQueryForm: s.ExecuteMySQLQueryForm, Ctx: s.C.Request.Context()}
	columns, data, duration, err := CalculateDuration(instanceCfg, executeApi.Execute)
	if err != nil {
		return responseData, err
	}

	global.App.DB.Model(&models.InsightDASRecords{}).
		Where("request_id=? and username=?", requestid.Get(s.C), s.Username).
		Updates(map[string]any{"ReturnRows": len(*data), "Duration": duration})

	responseData.Duration = fmt.Sprintf("%dms", duration)
	responseData.Data = data
	responseData.Columns = columns
	return responseData, nil
}
