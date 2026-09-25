package model

// Response — ответ диагностической ручки сервиса.
type Response struct {
	StatusCode  int
	ContentType string
	Body        []byte
	// Truncated — тело обрезано по лимиту байт
	Truncated bool
}

// PodTarget — под, в который идёт запрос напрямую (манифест сервиса и ручки из него:
// служебного порта в k8s Service обычно нет, а состояние у каждого пода своё).
type PodTarget struct {
	Namespace string
	Pod       string
	IP        string
	Port      int
}
