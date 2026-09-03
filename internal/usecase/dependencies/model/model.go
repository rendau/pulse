package model

import "time"

const (
	DirectionUpstream   = "upstream"
	DirectionDownstream = "downstream"
	DirectionBoth       = "both"
)

type GraphReq struct {
	Service   string
	Direction string
	Depth     int
}

// Graph — узлы и рёбра вокруг сервиса.
type Graph struct {
	Service   string
	Direction string
	Depth     int
	Nodes     []Node
	Edges     []Edge
	Truncated bool
	Errors    []SourceError
}

// Node — сервис каталога либо внешний адрес (External=true).
type Node struct {
	Name     string
	Title    string
	External bool
	// Health — краткий статус здоровья соседа (healthy | degraded | down | unknown); у внешних пусто
	Health   string
	PodsInfo string // «3/3 ready»
	Distance int
}

// Edge — сконфигурированная связь from → to с источником факта.
type Edge struct {
	From     string
	To       string
	Host     string
	Port     int32
	Scheme   string
	Source   string // env | configmap | kusec
	Keys     []string
	LastSeen time.Time
}

type SourceError struct {
	Source  string
	Message string
}
