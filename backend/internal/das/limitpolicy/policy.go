package limitpolicy

const (
	DefaultReturnRows uint64 = 200
	MaxReturnRows     uint64 = 1000
	ResultPageSize    int    = 30
)

func ClampRows(rows uint64) uint64 {
	if rows > MaxReturnRows {
		return MaxReturnRows
	}
	return rows
}
