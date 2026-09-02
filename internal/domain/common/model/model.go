package model

type ListParams struct {
	Page           int64
	PageSize       int64
	WithTotalCount bool
	OnlyCount      bool
	SortName       string
	Sort           []string
}
