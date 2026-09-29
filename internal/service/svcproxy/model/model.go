package model

// Response — ответ диагностической ручки сервиса.
type Response struct {
	StatusCode  int
	ContentType string
	Body        []byte
	// Truncated — тело обрезано по лимиту байт
	Truncated bool
}

// ServiceTarget — k8s Service, через который идёт запрос (манифест сервиса и ручки из него):
// отвечает любой готовый под за Service. Port — порт Service.
type ServiceTarget struct {
	Namespace string
	Service   string
	Port      int
}
