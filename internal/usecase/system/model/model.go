package model

import "time"

type Ping struct {
	Version     string
	GeneratedAt time.Time
	Sources     []SourceStatus
}

type SourceStatus struct {
	Name    string
	Status  string // ok | error | disabled
	Error   string
	Latency time.Duration
}
