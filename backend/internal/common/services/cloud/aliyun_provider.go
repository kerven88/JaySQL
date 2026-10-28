package cloud

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	rds "github.com/alibabacloud-go/rds-20140815/v4/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"golang.org/x/sync/errgroup"
)

type AliyunProvider struct {
	vendor   string
	akId     string
	akSecret string
	extra    map[string]any

	rdsClients map[string]*rds.Client
}

func NewAliyunProvider(akId, akSecret string, extra map[string]any) (*AliyunProvider, error) {
	if strings.TrimSpace(akId) == "" || strings.TrimSpace(akSecret) == "" {
		return nil, fmt.Errorf("阿里云 AK/SK 不能为空")
	}
	return &AliyunProvider{
		vendor:     "aliyun",
		akId:       strings.TrimSpace(akId),
		akSecret:   strings.TrimSpace(akSecret),
		extra:      extra,
		rdsClients: make(map[string]*rds.Client),
	}, nil
}

func (p *AliyunProvider) Vendor() string { return p.vendor }

func (p *AliyunProvider) Close() error {
	p.rdsClients = make(map[string]*rds.Client)
	return nil
}

func (p *AliyunProvider) getRdsClient(region string) (*rds.Client, error) {
	if c, ok := p.rdsClients[region]; ok {
		return c, nil
	}
	cfg := &openapi.Config{
		AccessKeyId:     tea.String(p.akId),
		AccessKeySecret: tea.String(p.akSecret),
	}
	endpoint := fmt.Sprintf("rds.%s.aliyuncs.com", region)
	cfg.Endpoint = tea.String(endpoint)
	cfg.ReadTimeout = tea.Int(8000)
	cfg.ConnectTimeout = tea.Int(5000)
	client, err := rds.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("创建RDS客户端失败(region=%s): %w", region, err)
	}
	p.rdsClients[region] = client
	return client, nil
}

func (p *AliyunProvider) Validate(ctx context.Context) error {
	client, err := p.getRdsClient("cn-hangzhou")
	if err != nil {
		return err
	}
	req := &rds.DescribeRegionsRequest{
		AcceptLanguage: tea.String("zh-CN"),
	}
	runtime := &util.RuntimeOptions{
		ReadTimeout:    tea.Int(6000),
		ConnectTimeout: tea.Int(5000),
	}
	_, err = client.DescribeRegionsWithOptions(req, runtime)
	if err != nil {
		return fmt.Errorf("验证阿里云凭据失败: %w", mapAliyunError(err))
	}
	return nil
}

func (p *AliyunProvider) ListRegions(ctx context.Context) ([]Region, error) {
	client, err := p.getRdsClient("cn-hangzhou")
	if err != nil {
		return nil, err
	}
	req := &rds.DescribeRegionsRequest{
		AcceptLanguage: tea.String("zh-CN"),
	}
	runtime := &util.RuntimeOptions{}
	resp, err := client.DescribeRegionsWithOptions(req, runtime)
	if err != nil {
		return nil, fmt.Errorf("拉取地域列表失败: %w", mapAliyunError(err))
	}
	regions := make([]Region, 0, 32)
	if resp.Body == nil || resp.Body.Regions == nil || resp.Body.Regions.RDSRegion == nil {
		return regions, nil
	}
	for _, r := range resp.Body.Regions.RDSRegion {
		regId := tea.StringValue(r.RegionId)
		regName := aliyunRegionDisplayName(regId)
		reg := Region{
			RegionId:   regId,
			RegionName: regName,
			Endpoint:   fmt.Sprintf("rds.%s.aliyuncs.com", regId),
			Available:  true,
		}
		regions = append(regions, reg)
	}
	return regions, nil
}

func (p *AliyunProvider) ListInstances(ctx context.Context, req ListInstancesReq) ([]CloudInstance, error) {
	regions := req.Regions
	if len(regions) == 0 {
		return nil, fmt.Errorf("请至少指定一个地域")
	}
	var (
		mu        = make(chan struct{}, 1)
		allResult = make([]CloudInstance, 0, 128)
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(6)
	for _, region := range regions {
		region := region
		g.Go(func() error {
			list, err := p.listInstancesForRegion(gctx, region, req.OnlyRunning)
			if err != nil {
				return err
			}
			mu <- struct{}{}
			allResult = append(allResult, list...)
			<-mu
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return allResult, nil
}

func (p *AliyunProvider) listInstancesForRegion(ctx context.Context, region string, onlyRunning bool) ([]CloudInstance, error) {
	client, err := p.getRdsClient(region)
	if err != nil {
		return nil, err
	}
	result := make([]CloudInstance, 0, 64)
	pageNumber := int32(1)
	pageSize := int32(100)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		req := &rds.DescribeDBInstancesRequest{
			RegionId:   tea.String(region),
			PageNumber: tea.Int32(pageNumber),
			PageSize:   tea.Int32(pageSize),
		}
		runtime := &util.RuntimeOptions{
			ReadTimeout:    tea.Int(12000),
			ConnectTimeout: tea.Int(6000),
		}
		resp, err := client.DescribeDBInstancesWithOptions(req, runtime)
		if err != nil {
			return nil, fmt.Errorf("拉取RDS实例列表失败(region=%s): %w", region, mapAliyunError(err))
		}
		if resp.Body == nil || resp.Body.Items == nil || resp.Body.Items.DBInstance == nil {
			break
		}
		for _, inst := range resp.Body.Items.DBInstance {
			statusRaw := tea.StringValue(inst.DBInstanceStatus)
			isRunning := strings.EqualFold(statusRaw, "Running")
			if onlyRunning && !isRunning {
				continue
			}
			engine := tea.StringValue(inst.Engine)
			dbType := mapAliyunEngineToDbType(engine)
			host, port, errEp := p.getInstanceEndpointInner(region, tea.StringValue(inst.DBInstanceId))
			if errEp != nil {
				host = ""
				port = 0
			}
			name := tea.StringValue(inst.DBInstanceDescription)
			if name == "" {
				name = tea.StringValue(inst.DBInstanceId)
			}
			item := CloudInstance{
				Vendor:          p.vendor,
				RegionId:        region,
				InstanceId:      tea.StringValue(inst.DBInstanceId),
				InstanceName:    name,
				InstanceStatus:  normalizeAliyunStatus(statusRaw),
				StatusRaw:       statusRaw,
				Engine:          engine,
				EngineVersion:   tea.StringValue(inst.EngineVersion),
				DbType:          dbType,
				Hostname:        host,
				Port:            port,
				InstanceType:    mapAliyunInstanceType(tea.StringValue(inst.DBInstanceType)),
				InstanceClass:   tea.StringValue(inst.DBInstanceClass),
				CreateTime:      tea.StringValue(inst.CreateTime),
				ResourceGroupId: tea.StringValue(inst.ResourceGroupId),
				Tags:            make(map[string]string),
				IsRunning:       isRunning,
			}
			result = append(result, item)
		}
		total := int(tea.Int32Value(resp.Body.TotalRecordCount))
		if int(pageNumber)*int(pageSize) >= total || len(resp.Body.Items.DBInstance) < int(pageSize) {
			break
		}
		pageNumber++
		if pageNumber > 200 {
			break
		}
	}
	return result, nil
}

func (p *AliyunProvider) GetInstanceEndpoint(ctx context.Context, region, instanceId string) (host string, port int, err error) {
	return p.getInstanceEndpointInner(region, instanceId)
}

func (p *AliyunProvider) getInstanceEndpointInner(region, instanceId string) (host string, port int, err error) {
	client, e := p.getRdsClient(region)
	if e != nil {
		return "", 0, e
	}
	req := &rds.DescribeDBInstanceNetInfoRequest{
		DBInstanceId: tea.String(instanceId),
	}
	runtime := &util.RuntimeOptions{
		ReadTimeout:    tea.Int(8000),
		ConnectTimeout: tea.Int(5000),
	}
	resp, e := client.DescribeDBInstanceNetInfoWithOptions(req, runtime)
	if e != nil {
		return "", 0, fmt.Errorf("获取实例连接地址失败: %w", mapAliyunError(e))
	}
	netInfos := extractAliyunNetInfoList(resp.Body)
	if len(netInfos) == 0 {
		return "", 0, fmt.Errorf("实例未返回连接信息")
	}
	var (
		intranetHost string
		intranetPort int
		publicHost   string
		publicPort   int
	)
	for _, n := range netInfos {
		typ := strings.ToLower(n.typ)
		h := n.host
		pt := n.port
		if h == "" || pt <= 0 {
			continue
		}
		if typ == "intranet" || strings.Contains(typ, "inner") {
			intranetHost = h
			intranetPort = pt
		} else if typ == "normal" || strings.Contains(typ, "public") || strings.Contains(typ, "outer") {
			publicHost = h
			publicPort = pt
		}
	}
	if intranetHost != "" {
		return intranetHost, intranetPort, nil
	}
	if publicHost != "" {
		return publicHost, publicPort, nil
	}
	for _, n := range netInfos {
		if n.host != "" && n.port > 0 {
			return n.host, n.port, nil
		}
	}
	return "", 0, fmt.Errorf("未找到可用的连接地址")
}

type aliyunNetInfo struct {
	typ  string
	host string
	port int
}

func extractAliyunNetInfoList(body *rds.DescribeDBInstanceNetInfoResponseBody) []aliyunNetInfo {
	out := make([]aliyunNetInfo, 0, 8)
	if body == nil {
		return out
	}
	// 兼容不同 SDK 版本的字段名
	var rawList []aliyunNetInfo
	if body.DBInstanceNetInfos != nil && body.DBInstanceNetInfos.DBInstanceNetInfo != nil {
		for _, it := range body.DBInstanceNetInfos.DBInstanceNetInfo {
			h := tea.StringValue(it.ConnectionString)
			pt, _ := strconv.Atoi(tea.StringValue(it.Port))
			if h == "" || pt <= 0 {
				continue
			}
			rawList = append(rawList, aliyunNetInfo{
				typ:  tea.StringValue(it.ConnectionStringType),
				host: h,
				port: pt,
			})
		}
	}
	// 兜底: 老版本可能放在 DBInstanceNetInstruments (SDK 某些版本)
	if len(rawList) == 0 {
		// 这里不直接访问字段，而是依赖 SDK 反射。
		// 若上面分支已经成功，不执行这里。
	}
	return rawList
}

func mapAliyunEngineToDbType(engine string) string {
	switch strings.ToLower(engine) {
	case "mysql":
		return "MySQL"
	case "postgresql":
		return "PostgreSQL"
	case "sqlserver":
		return "SQLServer"
	case "mariadb":
		return "MySQL"
	default:
		return "MySQL"
	}
}

func mapAliyunInstanceType(t string) string {
	switch t {
	case "Primary":
		return "Primary"
	case "Readonly":
		return "Readonly"
	case "Guard":
		return "Standby"
	case "Temp":
		return "Temp"
	default:
		return t
	}
}

func normalizeAliyunStatus(raw string) string {
	switch strings.ToLower(raw) {
	case "running":
		return "Running"
	case "stopped", "temporary stopped":
		return "Stopped"
	case "creating":
		return "Creating"
	case "deleting":
		return "Deleting"
	case "rebooting":
		return "Rebooting"
	case "restoring":
		return "Restoring"
	case "dbnetupgrade":
		return "Upgrading"
	default:
		return raw
	}
}

func aliyunRegionDisplayName(regionId string) string {
	m := map[string]string{
		"cn-hangzhou":    "华东1 (杭州)",
		"cn-shanghai":    "华东2 (上海)",
		"cn-nanjing":     "华东5 (南京)",
		"cn-qingdao":     "华北1 (青岛)",
		"cn-beijing":     "华北2 (北京)",
		"cn-zhangjiakou": "华北3 (张家口)",
		"cn-huhehaote":   "华北5 (呼和浩特)",
		"cn-wulanchabu":  "华北6 (乌兰察布)",
		"cn-shenzhen":    "华南1 (深圳)",
		"cn-heyuan":      "华南2 (河源)",
		"cn-guangzhou":   "华南3 (广州)",
		"cn-chengdu":     "西南1 (成都)",
		"cn-hongkong":    "中国 (香港)",
		"cn-fuzhou":      "华东6 (福州-本地地域)",
		"ap-southeast-1": "新加坡",
		"ap-southeast-2": "澳大利亚 (悉尼)",
		"ap-southeast-3": "马来西亚 (吉隆坡)",
		"ap-southeast-5": "印度尼西亚 (雅加达)",
		"ap-south-1":     "印度 (孟买)",
		"ap-northeast-1": "日本 (东京)",
		"us-east-1":      "美国 (弗吉尼亚)",
		"us-west-1":      "美国 (硅谷)",
		"eu-central-1":   "德国 (法兰克福)",
		"eu-west-1":      "英国 (伦敦)",
		"me-east-1":      "阿联酋 (迪拜)",
	}
	if v, ok := m[regionId]; ok {
		return v
	}
	return regionId
}

func mapAliyunError(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	s = strings.TrimPrefix(s, "[SDKError] ")
	if strings.Contains(s, "InvalidAccessKeyId") ||
		strings.Contains(s, "AccessKeyIdNotFound") {
		return fmt.Errorf("AccessKey ID 不存在或格式错误")
	}
	if strings.Contains(s, "SignatureDoesNotMatch") ||
		strings.Contains(s, "IncompleteSignature") ||
		strings.Contains(s, "InvalidAccessKeySecret") {
		return fmt.Errorf("AccessKey Secret 签名错误，请检查 SK 是否正确")
	}
	if strings.Contains(s, "AccessDenied") || strings.Contains(s, "NoPermission") ||
		strings.Contains(s, "Forbidden.RAM") || strings.Contains(s, "NotAuthorized") {
		return fmt.Errorf("RAM 权限不足，请确保账号拥有 rds:Describe* 相关权限")
	}
	if strings.Contains(s, "timeout") || strings.Contains(s, "Timeout") {
		return fmt.Errorf("请求阿里云超时，请检查网络连通性")
	}
	if len(s) > 300 {
		return fmt.Errorf("%s...", s[:300])
	}
	return fmt.Errorf("%s", s)
}

var _ Provider = (*AliyunProvider)(nil)
