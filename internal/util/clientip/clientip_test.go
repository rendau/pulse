package clientip

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:4321"
	assert.Equal(t, "10.0.0.5", FromRequest(r))

	r.Header.Set("X-Real-Ip", "10.1.1.1")
	assert.Equal(t, "10.1.1.1", FromRequest(r))

	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	assert.Equal(t, "203.0.113.7", FromRequest(r))

	r.Header.Set("Cf-Connecting-Ip", "198.51.100.2")
	assert.Equal(t, "198.51.100.2", FromRequest(r))
}
