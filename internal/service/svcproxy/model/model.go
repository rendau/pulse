package model

// Response — ответ диагностической ручки сервиса.
type Response struct {
	StatusCode  int
	ContentType string
	Body        []byte
	// Truncated — тело обрезано по лимиту байт
	Truncated bool
}
