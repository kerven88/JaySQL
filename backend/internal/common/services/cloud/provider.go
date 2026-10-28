package cloud

import (
	"context"
	"fmt"
)

type Provider interface {
	Vendor() string
	Validate(ctx context.Context) error
	ListRegions(ctx context.Context) ([]Region, error)
	ListInstances(ctx context.Context, req ListInstancesReq) ([]CloudInstance, error)
	GetInstanceEndpoint(ctx context.Context, region, instanceId string) (host string, port int, err error)
	Close() error
}

func NewProvider(vendor, akId, akSecret string, extra map[string]any) (Provider, error) {
	switch vendor {
	case "aliyun":
		return NewAliyunProvider(akId, akSecret, extra)
	case "huawei":
		return nil, fmt.Errorf("华为云 Provider 开发中")
	case "tencent":
		return nil, fmt.Errorf("腾讯云 Provider 开发中")
	default:
		return nil, fmt.Errorf("不支持的云厂商: %s", vendor)
	}
}
