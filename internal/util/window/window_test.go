package window

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	d, err := Parse("", Default, Max)
	require.NoError(t, err)
	assert.Equal(t, time.Hour, d)

	d, err = Parse("15m", Default, Max)
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, d)

	d, err = Parse("2d", Default, Max)
	require.NoError(t, err)
	assert.Equal(t, 48*time.Hour, d)

	_, err = Parse("8d", Default, Max)
	assert.Error(t, err)
	_, err = Parse("-1h", Default, Max)
	assert.Error(t, err)
	_, err = Parse("soon", Default, Max)
	assert.Error(t, err)
}

func TestFormat(t *testing.T) {
	assert.Equal(t, "7d", Format(7*24*time.Hour))
	assert.Equal(t, "24h", Format(24*time.Hour))
	assert.Equal(t, "90m", Format(90*time.Minute))
	assert.Equal(t, "15m", Format(15*time.Minute))
	assert.Equal(t, "1m30s", Format(90*time.Second))
}
