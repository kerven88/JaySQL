package services

import (
	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/performance/forms"
)

type GetInstanceOptionsService struct {
	Form     *forms.GetInstanceOptionsForm
	C        *gin.Context
	Username string
}

func (s *GetInstanceOptionsService) Run() (map[string]any, error) {
	instances, err := queryPerformanceInstances(s.Form.Search)
	if err != nil {
		return nil, err
	}

	list := make([]instanceOptionRow, 0, len(instances))
	for _, item := range instances {
		if s.Form.SupportedOnly && !item.Supported {
			continue
		}
		// 采集类页面（慢SQL分析、参数管理）只应看到真正采得到数据的实例
		if s.Form.CollectableOnly && !item.Collectable {
			continue
		}
		list = append(list, item)
	}

	return map[string]any{
		"supported_db_types": supportedDBTypes,
		"instances":          list,
	}, nil
}
