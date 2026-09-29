package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

// fakeK8s — свой под и Service его namespace; calls — сколько было запросов к API.
type fakeK8s struct {
	pod      *k8sModel.Pod
	podErr   error
	services []k8sModel.Service
	svcErr   error
	calls    int
}

func (f *fakeK8s) SelfPod(context.Context) (*k8sModel.Pod, error) {
	f.calls++
	return f.pod, f.podErr
}

func (f *fakeK8s) ListServices(context.Context, string) ([]k8sModel.Service, error) {
	f.calls++
	return f.services, f.svcErr
}

var selfPod = &k8sModel.Pod{
	Namespace: "default", Name: "pulse-69445c74fd-c86c8",
	Labels: map[string]string{"app": "pulse", "pod-template-hash": "69445c74fd"},
	Ports:  []k8sModel.PodPort{{Container: "pulse", Name: "http", Port: 80}, {Container: "pulse", Name: "system", Port: 3003}},
}

// Свои Service — селектор совпал с лейблами своего пода; порт — targetPort: число как есть,
// имя — порт контейнера своего пода.
func TestSelfPorts(t *testing.T) {
	app := map[string]string{"app": "pulse"}
	services := []k8sModel.Service{
		{Namespace: "default", Name: "pulse", Selector: app, Ports: []k8sModel.ServicePort{
			{Name: "http", Port: 80, TargetPort: "80"}, {Name: "system", Port: 3003, TargetPort: "3003"},
		}},
		{Namespace: "default", Name: "pulse-named", Selector: app, Ports: []k8sModel.ServicePort{
			{Name: "system", Port: 3013, TargetPort: "system"},
			{Name: "metrics", Port: 9090, TargetPort: "metrics"}, // такого порта у контейнера нет
		}},
		{Namespace: "default", Name: "pulse-default", Selector: app, Ports: []k8sModel.ServicePort{{Name: "system", Port: 4004}}},
		{Namespace: "default", Name: "pulse-agent", Selector: map[string]string{"app": "pulse-agent"}, Ports: []k8sModel.ServicePort{{Name: "system", Port: 3003}}},
		{Namespace: "stage", Name: "pulse", Selector: app, Ports: []k8sModel.ServicePort{{Name: "system", Port: 3003}}},
		{Namespace: "default", Name: "external", Ports: []k8sModel.ServicePort{{Name: "system", Port: 3003}}},
	}

	assert.Equal(t, map[string]int{
		"pulse:80":           80,
		"pulse:3003":         3003,
		"pulse-named:3013":   3003,
		"pulse-default:4004": 4004,
	}, selfPorts(selfPod, services))
}

// Свой Service — запрос уходит на localhost, на targetPort; остальные — через адрес Service.
func TestGetService_OwnServiceViaLocalhost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/.well-known/pulse", r.URL.Path)
		assert.Equal(t, "indexer", r.Header.Get("X-Pulse-Request-Id"))
		_, _ = w.Write([]byte(`{"pulse_manifest":1}`))
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)

	k8s := &fakeK8s{pod: selfPod, services: []k8sModel.Service{{
		Namespace: "default", Name: "pulse", Selector: map[string]string{"app": "pulse"},
		Ports: []k8sModel.ServicePort{{Name: "system", Port: 3003, TargetPort: port}},
	}}}
	s := New(k8s)

	own := svcproxyModel.ServiceTarget{Namespace: "default", Service: "pulse", Port: 3003}
	resp, err := s.GetService(context.Background(), own, "/.well-known/pulse", nil, map[string]string{"X-Pulse-Request-Id": "indexer"}, 1024)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.JSONEq(t, `{"pulse_manifest":1}`, string(resp.Body))

	for _, target := range []svcproxyModel.ServiceTarget{
		{Namespace: "default", Service: "pulse-agent", Port: 3003},
		{Namespace: "default", Service: "pulse", Port: 80},
		{Namespace: "stage", Service: "pulse", Port: 3003},
	} {
		_, local := s.localPort(context.Background(), target)
		assert.False(t, local, target)
	}
	assert.Equal(t, 2, k8s.calls, "свой под и Service — один раз, дальше из карты")
}

// Карта своих Service перечитывается раз в 10 минут; не прочиталась — прошлая карта и повтор
// через минуту; вне кластера и без k8s — своих Service нет.
func TestLocalPort_Refresh(t *testing.T) {
	own := svcproxyModel.ServiceTarget{Namespace: "default", Service: "pulse", Port: 3003}
	k8s := &fakeK8s{pod: selfPod, services: []k8sModel.Service{{
		Namespace: "default", Name: "pulse", Selector: map[string]string{"app": "pulse"},
		Ports: []k8sModel.ServicePort{{Name: "system", Port: 3003, TargetPort: "3003"}},
	}}}
	now := time.Now()
	s := New(k8s)
	s.now = func() time.Time { return now }

	port, ok := s.localPort(context.Background(), own)
	require.True(t, ok)
	assert.Equal(t, 3003, port)

	now = now.Add(9 * time.Minute)
	_, ok = s.localPort(context.Background(), own)
	assert.True(t, ok)
	assert.Equal(t, 2, k8s.calls, "до 10 минут — без запросов к API")

	// API не ответил — прошлая карта, повтор через минуту
	now = now.Add(2 * time.Minute)
	k8s.svcErr = errors.New("apiserver is unavailable")
	_, ok = s.localPort(context.Background(), own)
	assert.True(t, ok, "прошлая карта")
	assert.Equal(t, 4, k8s.calls)

	// Service сменил порт — видно после повтора
	now = now.Add(time.Minute)
	k8s.svcErr = nil
	k8s.services[0].Ports[0].TargetPort = "3013"
	port, ok = s.localPort(context.Background(), own)
	require.True(t, ok)
	assert.Equal(t, 3013, port)
	assert.Equal(t, 6, k8s.calls)

	// вне кластера своего пода нет
	outside := &fakeK8s{}
	s = New(outside)
	_, ok = s.localPort(context.Background(), own)
	assert.False(t, ok)
	assert.Equal(t, 1, outside.calls, "без своего пода Service не читаются")

	_, ok = New(nil).localPort(context.Background(), own)
	assert.False(t, ok)
}
