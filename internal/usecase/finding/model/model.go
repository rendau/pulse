// Package model — модели usecase находок об атаках.
package model

// ReportReq — находка от агента, который ищет атаки по журналу входящих запросов.
type ReportReq struct {
	Ip       string
	Severity string
	Title    string
	Text     string
}

// Report — находка в ленте pulse-agent.
type Report struct {
	Key          string
	Id           int64
	Deduplicated bool
}
