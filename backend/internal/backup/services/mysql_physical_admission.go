package services

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	dasDao "github.com/simajiong/jaysql/internal/das/dao"
)

type mysqlPhysicalTableEngineIssue struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	Engine string `json:"engine"`
}

type mysqlPhysicalAdmissionPlan struct {
	VersionFamily          string                          `json:"version_family,omitempty"`
	PrivilegeCheckUser     string                          `json:"privilege_check_user,omitempty"`
	ActiveRoles            []string                        `json:"active_roles,omitempty"`
	RequiredPrivileges     []string                        `json:"required_privileges,omitempty"`
	GrantedPrivileges      []string                        `json:"granted_privileges,omitempty"`
	MissingPrivileges      []string                        `json:"missing_privileges,omitempty"`
	PrivilegeNotes         []string                        `json:"privilege_notes,omitempty"`
	NonInnoDBTableCount    int                             `json:"non_innodb_table_count"`
	NonInnoDBTables        []mysqlPhysicalTableEngineIssue `json:"non_innodb_tables,omitempty"`
	Datadir                string                          `json:"datadir,omitempty"`
	DatadirCheckUser       string                          `json:"datadir_check_user,omitempty"`
	DatadirReadable        bool                            `json:"datadir_readable"`
	RedoSearchBase         string                          `json:"redo_search_base,omitempty"`
	RedoCheckUser          string                          `json:"redo_check_user,omitempty"`
	RedoLogPaths           []string                        `json:"redo_log_paths,omitempty"`
	UnreadableRedoLogPaths []string                        `json:"unreadable_redo_log_paths,omitempty"`
	UndoSearchBase         string                          `json:"undo_search_base,omitempty"`
	UndoCheckUser          string                          `json:"undo_check_user,omitempty"`
	UndoDirReadable        bool                            `json:"undo_dir_readable"`
	UndoLogPaths           []string                        `json:"undo_log_paths,omitempty"`
	UnreadableUndoLogPaths []string                        `json:"unreadable_undo_log_paths,omitempty"`
	DataHomeSearchBase     string                          `json:"data_home_search_base,omitempty"`
	DataCheckUser          string                          `json:"data_check_user,omitempty"`
	DataHomeDirReadable    bool                            `json:"data_home_dir_readable"`
	DataDirPaths           []string                        `json:"data_dir_paths,omitempty"`
	UnreadableDataDirPaths []string                        `json:"unreadable_data_dir_paths,omitempty"`
	DataFilePaths          []string                        `json:"data_file_paths,omitempty"`
	UnreadableDataFiles    []string                        `json:"unreadable_data_files,omitempty"`
	BlockingIssues         []string                        `json:"blocking_issues,omitempty"`
}

func mysqlPhysicalRequiredPrivileges(serverVersion string) ([]string, []string) {
	required := []string{"PROCESS", "RELOAD", "LOCK TABLES", "REPLICATION CLIENT"}
	notes := []string{}
	if family, err := mysqlPhysicalSupportedVersionFamily(serverVersion); err == nil {
		if family == "8.0" || family == "8.4" {
			required = append(required, "BACKUP_ADMIN")
		} else {
			notes = append(notes, "MySQL 5.7 不支持 BACKUP_ADMIN，已按 5.7 可用权限集校验。")
		}
	}
	sort.Strings(required)
	return required, notes
}

func probeMySQLPhysicalAdmissionFromDB(host string, port int, user string, password string, serverVersion string) (*mysqlPhysicalAdmissionPlan, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	db := dasDao.DB{
		User:     user,
		Password: password,
		Host:     host,
		Port:     port,
		Database: "",
		Params:   map[string]string{"charset": "utf8mb4", "parseTime": "True", "loc": "Local"},
		Ctx:      ctx,
	}
	family, _ := mysqlPhysicalSupportedVersionFamily(serverVersion)
	requiredPrivileges, privilegeNotes := mysqlPhysicalRequiredPrivileges(serverVersion)
	plan := &mysqlPhysicalAdmissionPlan{
		VersionFamily:      strings.TrimSpace(family),
		RequiredPrivileges: requiredPrivileges,
		PrivilegeNotes:     privilegeNotes,
	}
	_, currentRows, err := db.Query("select current_user() as current_user_name")
	if err != nil {
		return nil, fmt.Errorf("读取 MySQL 当前连接用户失败: %w", err)
	}
	grantee := ""
	if currentRows != nil && len(*currentRows) > 0 {
		plan.PrivilegeCheckUser = strings.TrimSpace(fmt.Sprintf("%v", (*currentRows)[0]["current_user_name"]))
		grantee = mysqlPrivilegeGrantee(plan.PrivilegeCheckUser)
	}
	if strings.TrimSpace(grantee) == "" {
		return nil, fmt.Errorf("未读取到 MySQL 当前连接用户")
	}
	grantedSet, grantNotes, activeRoles, err := probeMySQLPhysicalGrantedPrivileges(&db, grantee, plan.RequiredPrivileges)
	if err != nil {
		return nil, err
	}
	plan.ActiveRoles = append([]string(nil), activeRoles...)
	plan.PrivilegeNotes = append(plan.PrivilegeNotes, grantNotes...)
	for name := range grantedSet {
		plan.GrantedPrivileges = append(plan.GrantedPrivileges, name)
	}
	sort.Strings(plan.GrantedPrivileges)
	for _, required := range plan.RequiredPrivileges {
		if _, ok := grantedSet[required]; !ok {
			plan.MissingPrivileges = append(plan.MissingPrivileges, required)
		}
	}
	countQuery := `select count(*) as total
from information_schema.tables
where table_type = 'BASE TABLE'
  and table_schema not in ('information_schema','performance_schema','mysql','sys')
  and upper(coalesce(engine, '')) <> 'INNODB'`
	_, countRows, err := db.Query(countQuery)
	if err != nil {
		return nil, fmt.Errorf("统计非 InnoDB 表失败: %w", err)
	}
	if countRows != nil && len(*countRows) > 0 {
		plan.NonInnoDBTableCount, _ = strconv.Atoi(strings.TrimSpace(fmt.Sprintf("%v", (*countRows)[0]["total"])))
	}
	sampleQuery := `select table_schema, table_name, coalesce(engine, '') as engine
from information_schema.tables
where table_type = 'BASE TABLE'
  and table_schema not in ('information_schema','performance_schema','mysql','sys')
  and upper(coalesce(engine, '')) <> 'INNODB'
order by table_schema, table_name
limit 20`
	_, sampleRows, err := db.Query(sampleQuery)
	if err != nil {
		return nil, fmt.Errorf("读取非 InnoDB 表明细失败: %w", err)
	}
	for _, row := range derefRows(sampleRows) {
		plan.NonInnoDBTables = append(plan.NonInnoDBTables, mysqlPhysicalTableEngineIssue{
			Schema: strings.TrimSpace(fmt.Sprintf("%v", row["table_schema"])),
			Table:  strings.TrimSpace(fmt.Sprintf("%v", row["table_name"])),
			Engine: strings.TrimSpace(fmt.Sprintf("%v", row["engine"])),
		})
	}
	plan.BlockingIssues = append(plan.BlockingIssues, mysqlPhysicalAdmissionIssues(plan)...)
	return plan, nil
}

func probeMySQLPhysicalGrantedPrivileges(db *dasDao.DB, grantee string, required []string) (map[string]struct{}, []string, []string, error) {
	grantedSet := map[string]struct{}{}
	notes := []string{}
	activeRoles := []string{}

	if db != nil {
		if _, roleRows, err := db.Query("select current_role() as current_role_name"); err == nil {
			activeRoles = parseMySQLCurrentRoles(mysqlGrantFirstValue(roleRows))
			if len(activeRoles) > 0 {
				notes = append(notes, "当前激活角色: "+strings.Join(activeRoles, ", "))
			}
		} else {
			notes = append(notes, fmt.Sprintf("CURRENT_ROLE() 读取失败: %s", strings.TrimSpace(err.Error())))
		}
		if _, grantRows, err := db.Query("show grants"); err == nil {
			lines := mysqlGrantLinesFromRows(grantRows)
			parsed := parseMySQLPrivilegesFromGrantLines(lines, required)
			for name := range parsed {
				grantedSet[name] = struct{}{}
			}
			if len(lines) > 0 {
				notes = append(notes, "权限来源: SHOW GRANTS")
			}
		} else {
			notes = append(notes, fmt.Sprintf("SHOW GRANTS 读取失败，已回退 information_schema: %s", strings.TrimSpace(err.Error())))
		}
		if query := buildMySQLShowGrantsUsingQuery(activeRoles); query != "" {
			if _, roleGrantRows, err := db.Query(query); err == nil {
				lines := mysqlGrantLinesFromRows(roleGrantRows)
				parsed := parseMySQLPrivilegesFromGrantLines(lines, required)
				for name := range parsed {
					grantedSet[name] = struct{}{}
				}
				if len(lines) > 0 {
					notes = append(notes, "权限来源: SHOW GRANTS FOR CURRENT_USER() USING CURRENT_ROLE")
				}
			} else {
				notes = append(notes, fmt.Sprintf("SHOW GRANTS USING CURRENT_ROLE 读取失败: %s", strings.TrimSpace(err.Error())))
			}
		}
	}
	if len(grantedSet) > 0 {
		return grantedSet, uniqueStrings(notes), activeRoles, nil
	}

	if db == nil {
		return nil, uniqueStrings(notes), activeRoles, fmt.Errorf("MySQL 连接为空，无法读取权限")
	}
	_, privilegeRows, err := db.Query(fmt.Sprintf(
		"select privilege_type from information_schema.user_privileges where grantee = '%s' order by privilege_type",
		escapeSingleQuotes(grantee),
	))
	if err != nil {
		return nil, uniqueStrings(notes), activeRoles, fmt.Errorf("读取 MySQL 全局权限失败: %w", err)
	}
	for _, row := range derefRows(privilegeRows) {
		name := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", row["privilege_type"])))
		if name == "" {
			continue
		}
		grantedSet[name] = struct{}{}
	}
	notes = append(notes, "权限来源: information_schema.user_privileges")
	return grantedSet, uniqueStrings(notes), activeRoles, nil
}

func probeMySQLPhysicalPathsRemote(runner *sshRunner, osUser string, datadir string, redoBaseDir string, undoBaseDir string, dataHomeBaseDir string, extraDataDirs []string) (*mysqlPhysicalAdmissionPlan, error) {
	if runner == nil {
		return nil, fmt.Errorf("SSH runner 为空")
	}
	script := buildRemoteLogicalToolResolveScript(strings.TrimSpace(osUser), buildMySQLPhysicalPathProbeScript(datadir, redoBaseDir, undoBaseDir, dataHomeBaseDir, extraDataDirs))
	output, err := runner.Run(script)
	return finalizeMySQLPhysicalPathProbeOutput(datadir, output, err)
}

func probeMySQLPhysicalPathsLocal(ctx context.Context, osUser string, datadir string, redoBaseDir string, undoBaseDir string, dataHomeBaseDir string, extraDataDirs []string) (*mysqlPhysicalAdmissionPlan, error) {
	script := buildMySQLPhysicalPathProbeScript(datadir, redoBaseDir, undoBaseDir, dataHomeBaseDir, extraDataDirs)
	runCtx := ctx
	if runCtx == nil {
		runCtx = context.Background()
	}
	var (
		output string
		err    error
	)
	if strings.TrimSpace(osUser) != "" {
		output, err = runLocalCommandCaptureAsUser(runCtx, strings.TrimSpace(osUser), "", "/bin/sh", []string{"-lc", script}, nil, nil)
	} else {
		output, err = runLocalCommandCaptureAsDBUser(runCtx, "MySQL", "", "/bin/sh", []string{"-lc", script}, nil, nil)
	}
	return finalizeMySQLPhysicalPathProbeOutput(datadir, output, err)
}

func buildMySQLPhysicalPathProbeScript(datadir string, redoBaseDir string, undoBaseDir string, dataHomeBaseDir string, extraDataDirs []string) string {
	lines := []string{
		"set +e",
		"DATADIR=" + shellQuote(strings.TrimSpace(datadir)),
		"REDO_BASE_DIR=" + shellQuote(strings.TrimSpace(redoBaseDir)),
		"UNDO_BASE_DIR=" + shellQuote(strings.TrimSpace(undoBaseDir)),
		"DATA_HOME_BASE_DIR=" + shellQuote(strings.TrimSpace(dataHomeBaseDir)),
		`CHECK_USER=$(id -un 2>/dev/null || true)`,
		`REDO_SEARCH_BASE="$REDO_BASE_DIR"`,
		`UNDO_SEARCH_BASE="$UNDO_BASE_DIR"`,
		`DATA_HOME_SEARCH_BASE="$DATA_HOME_BASE_DIR"`,
		`echo CHECK_USER=${CHECK_USER}`,
		`echo DATADIR=${DATADIR}`,
		`echo REDO_BASE_DIR=${REDO_BASE_DIR}`,
		`echo UNDO_BASE_DIR=${UNDO_BASE_DIR}`,
		`echo DATA_HOME_BASE_DIR=${DATA_HOME_BASE_DIR}`,
		`if [ -n "$DATADIR" ] && [ -d "$DATADIR" ] && [ -r "$DATADIR" ] && [ -x "$DATADIR" ]; then echo DATADIR_READABLE=1; else echo DATADIR_READABLE=0; fi`,
		`if [ -n "$UNDO_BASE_DIR" ] && [ -d "$UNDO_BASE_DIR" ] && [ -r "$UNDO_BASE_DIR" ] && [ -x "$UNDO_BASE_DIR" ]; then echo UNDO_DIR_READABLE=1; else echo UNDO_DIR_READABLE=0; fi`,
		`REDO_FOUND=0`,
		`if [ -d "$REDO_BASE_DIR/#innodb_redo" ]; then`,
		`  REDO_SEARCH_BASE="$REDO_BASE_DIR/#innodb_redo"`,
		`  find "$REDO_BASE_DIR/#innodb_redo" -maxdepth 1 -type f -name '#ib_redo*' 2>/dev/null | sort | while IFS= read -r f; do`,
		`    [ -n "$f" ] || continue`,
		`    REDO_FOUND=1`,
		`    echo REDO_PATH=$f`,
		`    if [ -r "$f" ]; then echo REDO_READABLE=$f; else echo REDO_UNREADABLE=$f; fi`,
		`  done`,
		`else`,
		`  for f in "$REDO_BASE_DIR"/ib_logfile*; do`,
		`    [ -e "$f" ] || continue`,
		`    REDO_FOUND=1`,
		`    echo REDO_PATH=$f`,
		`    if [ -r "$f" ]; then echo REDO_READABLE=$f; else echo REDO_UNREADABLE=$f; fi`,
		`  done`,
		`fi`,
		`echo REDO_SEARCH_BASE=${REDO_SEARCH_BASE}`,
		`UNDO_FOUND=0`,
		`for f in "$UNDO_BASE_DIR"/undo_* "$UNDO_BASE_DIR"/*.ibu; do`,
		`  [ -e "$f" ] || continue`,
		`  UNDO_FOUND=1`,
		`  echo UNDO_PATH=$f`,
		`  if [ -r "$f" ]; then echo UNDO_READABLE=$f; else echo UNDO_UNREADABLE=$f; fi`,
		`done`,
		`echo UNDO_SEARCH_BASE=${UNDO_SEARCH_BASE}`,
		`check_data_dir() {`,
		`  dir="$1"`,
		`  [ -n "$dir" ] || return 0`,
		`  echo DATA_DIR=$dir`,
		`  if [ -d "$dir" ] && [ -r "$dir" ] && [ -x "$dir" ]; then`,
		`    echo DATA_DIR_READABLE=$dir`,
		`    find "$dir" -maxdepth 1 \( -name 'ibdata*' -o -name '*.ibd' -o -name '*.ibt' -o -name '*.cfg' \) -type f 2>/dev/null | sort | while IFS= read -r f; do`,
		`      [ -n "$f" ] || continue`,
		`      echo DATA_FILE=$f`,
		`      if [ -r "$f" ]; then echo DATA_FILE_READABLE=$f; else echo DATA_FILE_UNREADABLE=$f; fi`,
		`    done`,
		`  else`,
		`    echo DATA_DIR_UNREADABLE=$dir`,
		`  fi`,
		`}`,
		`check_data_dir "$DATA_HOME_BASE_DIR"`,
	}
	for _, dir := range uniqueStrings(extraDataDirs) {
		lines = append(lines, "check_data_dir "+shellQuote(strings.TrimSpace(dir)))
	}
	lines = append(lines, `echo DATA_HOME_SEARCH_BASE=${DATA_HOME_SEARCH_BASE}`)
	return strings.Join(lines, "\n")
}

func finalizeMySQLPhysicalPathProbeOutput(datadir string, output string, runErr error) (*mysqlPhysicalAdmissionPlan, error) {
	plan := &mysqlPhysicalAdmissionPlan{
		Datadir: strings.TrimSpace(datadir),
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		text := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(text, "CHECK_USER="):
			user := strings.TrimSpace(strings.TrimPrefix(text, "CHECK_USER="))
			plan.DatadirCheckUser = user
			plan.RedoCheckUser = user
			plan.UndoCheckUser = user
			plan.DataCheckUser = user
		case strings.HasPrefix(text, "DATADIR="):
			plan.Datadir = strings.TrimSpace(strings.TrimPrefix(text, "DATADIR="))
		case strings.HasPrefix(text, "REDO_BASE_DIR="):
			plan.RedoSearchBase = strings.TrimSpace(strings.TrimPrefix(text, "REDO_BASE_DIR="))
		case strings.HasPrefix(text, "UNDO_BASE_DIR="):
			plan.UndoSearchBase = strings.TrimSpace(strings.TrimPrefix(text, "UNDO_BASE_DIR="))
		case strings.HasPrefix(text, "DATA_HOME_BASE_DIR="):
			plan.DataHomeSearchBase = strings.TrimSpace(strings.TrimPrefix(text, "DATA_HOME_BASE_DIR="))
		case strings.HasPrefix(text, "DATADIR_READABLE="):
			plan.DatadirReadable = strings.TrimSpace(strings.TrimPrefix(text, "DATADIR_READABLE=")) == "1"
		case strings.HasPrefix(text, "UNDO_DIR_READABLE="):
			plan.UndoDirReadable = strings.TrimSpace(strings.TrimPrefix(text, "UNDO_DIR_READABLE=")) == "1"
		case strings.HasPrefix(text, "REDO_SEARCH_BASE="):
			plan.RedoSearchBase = strings.TrimSpace(strings.TrimPrefix(text, "REDO_SEARCH_BASE="))
		case strings.HasPrefix(text, "UNDO_SEARCH_BASE="):
			plan.UndoSearchBase = strings.TrimSpace(strings.TrimPrefix(text, "UNDO_SEARCH_BASE="))
		case strings.HasPrefix(text, "DATA_HOME_SEARCH_BASE="):
			plan.DataHomeSearchBase = strings.TrimSpace(strings.TrimPrefix(text, "DATA_HOME_SEARCH_BASE="))
		case strings.HasPrefix(text, "DATA_DIR="):
			plan.DataDirPaths = append(plan.DataDirPaths, strings.TrimSpace(strings.TrimPrefix(text, "DATA_DIR=")))
		case strings.HasPrefix(text, "DATA_DIR_READABLE="):
			plan.DataHomeDirReadable = true
		case strings.HasPrefix(text, "DATA_DIR_UNREADABLE="):
			plan.UnreadableDataDirPaths = append(plan.UnreadableDataDirPaths, strings.TrimSpace(strings.TrimPrefix(text, "DATA_DIR_UNREADABLE=")))
		case strings.HasPrefix(text, "DATA_FILE="):
			plan.DataFilePaths = append(plan.DataFilePaths, strings.TrimSpace(strings.TrimPrefix(text, "DATA_FILE=")))
		case strings.HasPrefix(text, "DATA_FILE_UNREADABLE="):
			plan.UnreadableDataFiles = append(plan.UnreadableDataFiles, strings.TrimSpace(strings.TrimPrefix(text, "DATA_FILE_UNREADABLE=")))
		case strings.HasPrefix(text, "REDO_PATH="):
			plan.RedoLogPaths = append(plan.RedoLogPaths, strings.TrimSpace(strings.TrimPrefix(text, "REDO_PATH=")))
		case strings.HasPrefix(text, "REDO_UNREADABLE="):
			plan.UnreadableRedoLogPaths = append(plan.UnreadableRedoLogPaths, strings.TrimSpace(strings.TrimPrefix(text, "REDO_UNREADABLE=")))
		case strings.HasPrefix(text, "UNDO_PATH="):
			plan.UndoLogPaths = append(plan.UndoLogPaths, strings.TrimSpace(strings.TrimPrefix(text, "UNDO_PATH=")))
		case strings.HasPrefix(text, "UNDO_UNREADABLE="):
			plan.UnreadableUndoLogPaths = append(plan.UnreadableUndoLogPaths, strings.TrimSpace(strings.TrimPrefix(text, "UNDO_UNREADABLE=")))
		}
	}
	if runErr != nil && !strings.Contains(strings.TrimSpace(output), "DATADIR_READABLE=") {
		return nil, fmt.Errorf("检查 MySQL datadir/redo 文件可读性失败: %w", runErr)
	}
	plan.BlockingIssues = append(plan.BlockingIssues, mysqlPhysicalAdmissionIssues(plan)...)
	return plan, nil
}

func mysqlPhysicalAdmissionIssues(plan *mysqlPhysicalAdmissionPlan) []string {
	if plan == nil {
		return nil
	}
	issues := []string{}
	if len(plan.MissingPrivileges) > 0 {
		issues = append(issues, fmt.Sprintf("缺少物理备份所需权限: %s", strings.Join(plan.MissingPrivileges, ", ")))
	}
	if plan.NonInnoDBTableCount > 0 {
		issues = append(issues, fmt.Sprintf("存在 %d 张非 InnoDB 表，当前物理备份仅支持 InnoDB", plan.NonInnoDBTableCount))
	}
	if !plan.DatadirReadable {
		issues = append(issues, fmt.Sprintf("执行用户无法读取 datadir: %s", strings.TrimSpace(plan.Datadir)))
	}
	if len(plan.RedoLogPaths) == 0 {
		issues = append(issues, fmt.Sprintf("未在默认 redo 目录检测到可读 redo 文件: %s", displayOrDefault(strings.TrimSpace(plan.RedoSearchBase), strings.TrimSpace(plan.Datadir))))
	}
	if len(plan.UnreadableRedoLogPaths) > 0 {
		issues = append(issues, fmt.Sprintf("执行用户无法读取 redo 文件: %s", strings.Join(plan.UnreadableRedoLogPaths, ", ")))
	}
	if strings.TrimSpace(plan.UndoSearchBase) != "" && !plan.UndoDirReadable {
		issues = append(issues, fmt.Sprintf("执行用户无法读取 undo 目录: %s", strings.TrimSpace(plan.UndoSearchBase)))
	}
	if len(plan.UnreadableUndoLogPaths) > 0 {
		issues = append(issues, fmt.Sprintf("执行用户无法读取 undo 文件: %s", strings.Join(plan.UnreadableUndoLogPaths, ", ")))
	}
	if strings.TrimSpace(plan.DataHomeSearchBase) != "" && len(plan.UnreadableDataDirPaths) > 0 {
		issues = append(issues, fmt.Sprintf("执行用户无法读取 InnoDB 数据目录: %s", strings.Join(plan.UnreadableDataDirPaths, ", ")))
	}
	if len(plan.UnreadableDataFiles) > 0 {
		issues = append(issues, fmt.Sprintf("执行用户无法读取 InnoDB 数据文件: %s", strings.Join(plan.UnreadableDataFiles, ", ")))
	}
	return uniqueStrings(issues)
}

func validateMySQLPhysicalAdmission(plan *mysqlPhysicalExecutionPlan) error {
	if plan == nil || plan.Admission == nil {
		return nil
	}
	if len(plan.Admission.BlockingIssues) == 0 {
		return nil
	}
	return fmt.Errorf(strings.Join(plan.Admission.BlockingIssues, "; "))
}

func derefRows(rows *[]map[string]interface{}) []map[string]interface{} {
	if rows == nil {
		return nil
	}
	return *rows
}

func mysqlPrivilegeGrantee(currentUser string) string {
	value := strings.TrimSpace(currentUser)
	if value == "" {
		return ""
	}
	parts := strings.SplitN(value, "@", 2)
	if len(parts) != 2 {
		return "'" + escapeSingleQuotes(value) + "'"
	}
	return fmt.Sprintf("'%s'@'%s'", escapeSingleQuotes(parts[0]), escapeSingleQuotes(parts[1]))
}

func mysqlGrantLinesFromRows(rows *[]map[string]interface{}) []string {
	lines := []string{}
	for _, row := range derefRows(rows) {
		for _, value := range row {
			text := strings.TrimSpace(fmt.Sprintf("%v", value))
			if text == "" {
				continue
			}
			lines = append(lines, text)
		}
	}
	return uniqueStrings(lines)
}

func mysqlGrantFirstValue(rows *[]map[string]interface{}) string {
	for _, row := range derefRows(rows) {
		for _, value := range row {
			text := mysqlPhysicalStringValue(value)
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func parseMySQLPrivilegesFromGrantLines(lines []string, required []string) map[string]struct{} {
	granted := map[string]struct{}{}
	requiredSet := map[string]struct{}{}
	for _, item := range required {
		name := strings.ToUpper(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		requiredSet[name] = struct{}{}
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		if !strings.HasPrefix(upper, "GRANT ") || !strings.Contains(upper, " ON ") {
			continue
		}
		privSegment := strings.TrimSpace(line[len("GRANT "):])
		onIdx := strings.Index(strings.ToUpper(privSegment), " ON ")
		if onIdx <= 0 {
			continue
		}
		privSegment = strings.TrimSpace(privSegment[:onIdx])
		if strings.EqualFold(privSegment, "USAGE") {
			continue
		}
		if strings.EqualFold(privSegment, "ALL PRIVILEGES") {
			for name := range requiredSet {
				granted[name] = struct{}{}
			}
			continue
		}
		for _, item := range strings.Split(privSegment, ",") {
			name := normalizeMySQLPrivilegeName(item)
			if name == "" {
				continue
			}
			if _, ok := requiredSet[name]; ok {
				granted[name] = struct{}{}
			}
		}
	}
	return granted
}

func normalizeMySQLPrivilegeName(raw string) string {
	text := strings.ToUpper(strings.TrimSpace(raw))
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	switch text {
	case "GRANT", "OPTION", "ALL", "PRIVILEGES", "USAGE":
		return ""
	default:
		return text
	}
}

func parseMySQLCurrentRoles(raw string) []string {
	text := strings.TrimSpace(raw)
	if text == "" || strings.EqualFold(text, "NONE") {
		return nil
	}
	parts := strings.Split(text, ",")
	roles := make([]string, 0, len(parts))
	for _, part := range parts {
		role := normalizeMySQLRoleIdentity(part)
		if role == "" {
			continue
		}
		roles = append(roles, role)
	}
	return uniqueStrings(roles)
}

func buildMySQLShowGrantsUsingQuery(activeRoles []string) string {
	roles := uniqueStrings(activeRoles)
	if len(roles) == 0 {
		return ""
	}
	return "show grants for current_user() using " + strings.Join(roles, ", ")
}

func normalizeMySQLRoleIdentity(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" || strings.EqualFold(text, "NONE") {
		return ""
	}
	text = strings.Trim(text, "`' ")
	if text == "" {
		return ""
	}
	roleName := text
	roleHost := "%"
	if strings.Contains(text, "@") {
		parts := strings.SplitN(text, "@", 2)
		roleName = strings.Trim(parts[0], "`' ")
		roleHost = strings.Trim(parts[1], "`' ")
		if roleHost == "" {
			roleHost = "%"
		}
	}
	if roleName == "" {
		return ""
	}
	return fmt.Sprintf("'%s'@'%s'", escapeSingleQuotes(roleName), escapeSingleQuotes(roleHost))
}
