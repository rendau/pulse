package jobprefix

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrefixes(t *testing.T) {
	jobs := []string{"nightly-product-sync-1-9da3", "nightly-delivery-fetch-1-2249"}

	assert.Equal(t, []string{"nightly-"}, Prefixes(jobs, []string{"orchestrator-6c84-pg8rz"}))
	// один Job — префикс семейства (без хэша попытки)
	assert.Equal(t, []string{"nightly-sync-1-"}, Prefixes([]string{"nightly-sync-1-9da3"}, nil))
	// общего префикса нет — по семейству
	assert.Equal(t, []string{"alpha-1-", "beta-2-"}, Prefixes([]string{"beta-2-bbb", "alpha-1-aaa"}, nil))
	// общий захватил бы чужой под — мельче; конфликтные отбрасываются
	assert.Equal(t, []string{"nightly-delivery-fetch-1-", "nightly-product-sync-1-"}, Prefixes(jobs, []string{"nightly-other-app-7d9f-q2"}))
	assert.Empty(t, Prefixes([]string{"nightly-sync-1-9da3"}, []string{"nightly-sync-1-evil-x"}))
	assert.Nil(t, Prefixes(nil, nil))
}

func TestJobName(t *testing.T) {
	assert.Equal(t, "a-1", JobName(map[string]string{"batch.kubernetes.io/job-name": "a-1"}))
	assert.Equal(t, "b-2", JobName(map[string]string{"job-name": "b-2"}))
	assert.Empty(t, JobName(nil))
}
