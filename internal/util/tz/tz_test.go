package tz

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIn(t *testing.T) {
	got := In(time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC))
	assert.Equal(t, "2026-09-24T15:00:00+05:00", got.Format(time.RFC3339))

	assert.True(t, In(time.Time{}).IsZero())
	assert.Equal(t, time.UTC, In(time.Time{}).Location())
}

func TestInPtr(t *testing.T) {
	assert.Nil(t, InPtr(nil))

	got := InPtr(new(time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)))
	require.NotNil(t, got)
	assert.Equal(t, "2026-09-24T15:00:00+05:00", got.Format(time.RFC3339))
}

func TestParseEnd(t *testing.T) {
	end, day, err := ParseEnd("2026-09-20")
	require.NoError(t, err)
	assert.True(t, day)
	assert.Equal(t, "2026-09-21T00:00:00+05:00", end.Format(time.RFC3339), "конец суток по Алматы")

	end, day, err = ParseEnd("2026-09-20T16:00")
	require.NoError(t, err)
	assert.False(t, day)
	assert.Equal(t, "2026-09-20T16:00:00+05:00", end.Format(time.RFC3339))

	end, _, err = ParseEnd("2026-09-20T16:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, 16, end.Hour())

	_, _, err = ParseEnd("вчера")
	require.Error(t, err)
}
