package forms

import "github.com/simajiong/jaysql/pkg/pagination"

type GetHistoryForm struct {
	PaginationQ pagination.Pagination
	Search      string `form:"search"`
	StartDate   string `form:"start_date"`
	EndDate     string `form:"end_date"`
}
