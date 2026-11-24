// Package model — контрактные модели клиента pulse-agent.
package model

import "errors"

// ErrRateLimited — pulse-agent ответил 429: лимит находок в час исчерпан.
var ErrRateLimited = errors.New("pulse-agent: findings rate limit exceeded")

// Finding — находка для ленты pulse-agent. Key — ключ склейки повторов (security:<адрес>).
type Finding struct {
	Key      string
	Kind     string
	Severity string
	Title    string
	Text     string
	Source   string
}

// FindingResult — запись ленты: Deduplicated — склеена с уже открытой находкой по Key.
type FindingResult struct {
	Id           int64
	Deduplicated bool
}
