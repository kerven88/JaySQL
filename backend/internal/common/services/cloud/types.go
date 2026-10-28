package cloud

type Region struct {
	RegionId   string `json:"region_id"`
	RegionName string `json:"region_name"`
	Endpoint   string `json:"endpoint"`
	Available  bool   `json:"available"`
}

type CloudInstance struct {
	Vendor          string            `json:"vendor"`
	RegionId        string            `json:"region_id"`
	InstanceId      string            `json:"instance_id"`
	InstanceName    string            `json:"instance_name"`
	InstanceStatus  string            `json:"instance_status"`
	StatusRaw       string            `json:"status_raw"`
	Engine          string            `json:"engine"`
	EngineVersion   string            `json:"engine_version"`
	DbType          string            `json:"db_type"`
	Hostname        string            `json:"hostname"`
	Port            int               `json:"port"`
	PublicHostname  string            `json:"public_hostname"`
	PublicPort      int               `json:"public_port"`
	InstanceType    string            `json:"instance_type"`
	InstanceClass   string            `json:"instance_class"`
	CreateTime      string            `json:"create_time"`
	ResourceGroupId string            `json:"resource_group_id"`
	Tags            map[string]string `json:"tags"`
	IsRunning       bool              `json:"is_running"`
}

type ListInstancesReq struct {
	Regions     []string
	OnlyRunning bool
	Filters     map[string]any
}
