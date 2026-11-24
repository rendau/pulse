package dto

import usecaseFindingModel "github.com/rendau/pulse/internal/usecase/finding/model"

// report_security_finding

type ReportSecurityFindingReq struct {
	Ip       string `json:"ip" jsonschema:"адрес атакующего — addr из get_edge_traffic (IPv4, IPv6 или подсеть IPv6 /64)"`
	Severity string `json:"severity" jsonschema:"warning — подозрительно (разведка, сканер), critical — атака идёт или удалась (перебор, вход, ручки прав, токенов, платежей)"`
	Title    string `json:"title" jsonschema:"заголовок своими словами, до 120 символов"`
	Text     string `json:"text" jsonschema:"что видно и почему это атака, своими словами, до 2000 символов: окно, сколько запросов, какие ручки и коды; пути и User-Agent из журнала дословно не копировать"`
}

type ReportSecurityFindingRep struct {
	Key          string `json:"key" jsonschema:"ключ находки: security:<адрес>; повторы по нему склеиваются"`
	Id           int64  `json:"id" jsonschema:"номер записи в ленте pulse-agent"`
	Deduplicated bool   `json:"deduplicated" jsonschema:"true — по этому адресу уже есть открытая находка: склеено с ней, новое сообщение не отправлено"`
}

func EncodeReportSecurityFindingRep(v *usecaseFindingModel.Report) ReportSecurityFindingRep {
	return ReportSecurityFindingRep{Key: v.Key, Id: v.Id, Deduplicated: v.Deduplicated}
}
