// Package model — модели usecase журнала входящих запросов (сводка внешнего трафика, история адреса).
package model

import "time"

type TrafficReq struct {
	Window time.Duration
	End    time.Time // нулевое — сейчас
	Host   string    // пусто — все домены
	Top    int
}

// Traffic — сводка внешних запросов за окно по адресам клиентов.
type Traffic struct {
	Start, End time.Time
	Host       string
	Requests   float64
	Statuses   []Count // по классам кода: 2xx, 3xx, 4xx, 5xx
	// ClientsTotal — разных адресов за окно (0 — не посчитано целиком)
	ClientsTotal int
	Clients      []Client
	Own          []Client
	Signatures   []SignatureClient
	Sensitive    []SensitiveEndpoint
	Partial      bool
	Errors       []SourceError
	Queries      int
	Took         time.Duration
	CachedAt     time.Time
}

type Count struct {
	Value string
	Count float64
}

// Client — адрес клиента (IPv4 или подсеть IPv6 /64) и его трафик за окно.
type Client struct {
	Addr     string
	Own      string // метка своего адреса; пусто — чужой
	Requests float64
	// Share4xx/Share5xx — nil: подробности не считались
	Share4xx, Share5xx *float64
	// DistinctPaths — разных путей (сегменты с цифрами склеены); DistinctPathsMore — не меньше
	DistinctPaths     int
	DistinctPathsMore bool
	Hosts             []Count
	UserAgents        []Count
	Countries         []Count
	SignatureHits     float64
	SensitiveHits     float64
	// New — адреса не было за NewChecked до окна; nil — не проверялся
	New        *bool
	NewChecked time.Duration
}

// SignatureClient — адрес, в путях которого есть признаки взлома.
type SignatureClient struct {
	Addr       string
	Own        string
	Hits       float64
	Requests   float64
	Signatures []Count
}

// SensitiveEndpoint — обращения к чувствительной ручке (часть пути из конфига) по адресам.
type SensitiveEndpoint struct {
	Endpoint string
	Requests float64
	// Clients — разных адресов; ClientsMore — их больше (показаны только адреса из топа)
	Clients     int
	ClientsMore bool
	Top         []SensitiveClient
}

type SensitiveClient struct {
	Addr string
	Own  string
	Hits float64
}

type SourceError struct {
	Source  string
	Message string
}

type ActivityReq struct {
	Client string
	Window time.Duration
	End    time.Time
}

// Activity — сжатая хронология запросов одного адреса.
type Activity struct {
	Client     string
	Own        string
	Start, End time.Time
	Requests   int
	Internal   int // из них через внутреннюю дверь
	FirstSeen  time.Time
	LastSeen   time.Time
	Statuses   []Count
	Hosts      []Count
	UserAgents []Count
	Referers   []Count
	Countries  []Count
	Signatures []Count
	Sensitive  []Count
	Groups     []ActivityGroup
	// TotalGroups — групп всего; Truncated — показаны не все
	TotalGroups int
	Truncated   bool
	// LinesTruncated — строк больше лимита: хронология только с CoveredFrom
	LinesTruncated bool
	CoveredFrom    time.Time
}

// ActivityGroup — одинаковые запросы: метод + домен + путь (id склеены) + код.
type ActivityGroup struct {
	Method     string
	Host       string
	Path       string
	Status     int
	Internal   bool
	Count      int
	First      time.Time
	Last       time.Time
	Signatures []string
	Sensitive  string
}
