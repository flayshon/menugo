package data

import (
	"math"

	"menugo.flayshon.com/internal/validator"
)

// Pagination is a page request for a list endpoint.
type Pagination struct {
	Page     int
	PageSize int
}

func (p Pagination) limit() int {
	return p.PageSize
}

func (p Pagination) offset() int {
	return (p.Page - 1) * p.PageSize
}

func ValidatePagination(v *validator.Validator, p Pagination) {
	v.Check(p.Page > 0, "page", "must be greater than zero")
	v.Check(p.Page <= 10_000_000, "page", "must be a maximum of 10 million")
	v.Check(p.PageSize > 0, "page_size", "must be greater than zero")
	v.Check(p.PageSize <= 100, "page_size", "must be a maximum of 100")
}

// Metadata describes a page of results.
type Metadata struct {
	CurrentPage  int
	PageSize     int
	FirstPage    int
	LastPage     int
	TotalRecords int
}

func calculateMetadata(totalRecords int, p Pagination) Metadata {
	if totalRecords == 0 {
		return Metadata{}
	}
	return Metadata{
		CurrentPage:  p.Page,
		PageSize:     p.PageSize,
		FirstPage:    1,
		LastPage:     int(math.Ceil(float64(totalRecords) / float64(p.PageSize))),
		TotalRecords: totalRecords,
	}
}
