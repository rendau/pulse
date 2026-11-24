// Package model — входящий запрос из журнала edge-прокси (traefik access log) и известные адреса.
package model

import (
	"net/netip"
	"time"
)

// Request — одна строка журнала входящих запросов. Path, UserAgent, Referer — текст из
// запроса клиента: доверять ему нельзя, наружу — только после Clean.
type Request struct {
	TS time.Time
	// Client — адрес клиента: заголовок Cloudflare (Cf-Connecting-Ip), иначе адрес соединения
	Client    string
	Host      string
	Method    string
	Path      string
	Status    int
	Internal  bool // пришёл через внутреннюю дверь (роутер внутренней точки входа)
	UserAgent string
	Referer   string
	Country   string // страна по Cloudflare (Cf-Ipcountry), если домен за Cloudflare
}

// Known — свой адрес или подсеть с меткой («офис», «выход кластера»).
type Known struct {
	Prefix netip.Prefix
	Label  string
}

// Signature — признак взлома в пути запроса: обход каталогов, служебные файлы, документация API.
type Signature struct {
	Name string
	// Substrings — подстроки для быстрого предфильтра строк в Loki (надмножество Regex):
	// у Loki нет индекса по словам, и подстрока проверяется в разы быстрее регэкспа
	Substrings []string
	// Regex — точное условие по пути (RE2, без якорей)
	Regex string
}
