// Package service — журнал входящих запросов edge-прокси (traefik, JSON access log): разбор
// строки, адрес клиента, свои адреса, признаки взлома, нормализация пути и очистка текста
// из запроса. Чистые функции без обращения к источникам.
package service

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/rendau/pulse/internal/domain/edge/model"
)

// Поля JSON access log traefik (accessLog.format=json, заголовки — request_<Имя>).
const (
	FieldClientHost = "ClientHost"
	FieldHost       = "RequestHost"
	FieldMethod     = "RequestMethod"
	FieldPath       = "RequestPath"
	FieldStatus     = "DownstreamStatus"
	FieldRouter     = "RouterName"
	FieldUserAgent  = "request_User-Agent"
	FieldReferer    = "request_Referer"
	FieldCfClientIp = "request_Cf-Connecting-Ip"
	FieldCfCountry  = "request_Cf-Ipcountry"
)

type accessLine struct {
	ClientHost string `json:"ClientHost"`
	Host       string `json:"RequestHost"`
	Method     string `json:"RequestMethod"`
	Path       string `json:"RequestPath"`
	Status     int    `json:"DownstreamStatus"`
	Router     string `json:"RouterName"`
	UserAgent  string `json:"request_User-Agent"`
	Referer    string `json:"request_Referer"`
	CfClientIp string `json:"request_Cf-Connecting-Ip"`
	CfCountry  string `json:"request_Cf-Ipcountry"`
	StartLocal string `json:"StartLocal"`
	StartUTC   string `json:"StartUTC"`
}

// ParseRequest разбирает строку access log. internalRouterPrefix — начало имени роутера
// внутренней двери (пусто — внутренней двери нет). ts — время строки в Loki: запасное, если
// в строке нет времени начала запроса. Не JSON или без адреса клиента — false.
func ParseRequest(line string, ts time.Time, internalRouterPrefix string) (model.Request, bool) {
	var a accessLine
	if err := json.Unmarshal([]byte(line), &a); err != nil {
		return model.Request{}, false
	}
	client := strings.TrimSpace(a.CfClientIp)
	if client == "" {
		client = strings.TrimSpace(a.ClientHost)
	}
	if client == "" {
		return model.Request{}, false
	}

	req := model.Request{
		TS:        ts,
		Client:    client,
		Host:      a.Host,
		Method:    a.Method,
		Path:      a.Path,
		Status:    a.Status,
		Internal:  internalRouterPrefix != "" && strings.HasPrefix(a.Router, internalRouterPrefix),
		UserAgent: a.UserAgent,
		Referer:   a.Referer,
		Country:   strings.TrimSpace(a.CfCountry),
	}
	for _, s := range []string{a.StartLocal, a.StartUTC} {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			req.TS = t
			break
		}
	}
	return req, true
}

// StatusClass — класс кода ответа: "2xx" … "5xx"; вне 100–599 — "other".
func StatusClass(status int) string {
	if status < 100 || status > 599 {
		return "other"
	}
	return string(rune('0'+status/100)) + "xx"
}
