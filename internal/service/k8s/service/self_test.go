package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Свой под: namespace — из файла ServiceAccount, имя — hostname.
func TestSelfIdentity(t *testing.T) {
	file := filepath.Join(t.TempDir(), "namespace")
	require.NoError(t, os.WriteFile(file, []byte("default\n"), 0o600))
	hostname := func() (string, error) { return "pulse-69445c74fd-c86c8", nil }

	namespace, name, err := selfIdentity(file, hostname)
	require.NoError(t, err)
	assert.Equal(t, "default", namespace)
	assert.Equal(t, "pulse-69445c74fd-c86c8", name)

	_, _, err = selfIdentity(filepath.Join(t.TempDir(), "absent"), hostname)
	require.Error(t, err, "не в поде — файла нет")
	_, _, err = selfIdentity(file, func() (string, error) { return "", errors.New("no hostname") })
	require.Error(t, err)
}

// Вне кластера своего пода нет — в API не ходим.
func TestSelfPod_OutsideCluster(t *testing.T) {
	s := &Service{initErr: errors.New("kubeconfig is broken")}
	pod, err := s.SelfPod(context.Background())
	require.NoError(t, err)
	assert.Nil(t, pod)
}
