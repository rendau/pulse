package service

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
)

// podLogsLimitBytes — потолок на один контейнер: хвост логов, а не весь файл kubelet'а.
const podLogsLimitBytes = 2 << 20

// PodLogs — строки контейнера пода не старше since, не больше tail последних (kubectl logs
// --timestamps --since-time --tail); previous — прошлый запуск контейнера (до рестарта).
// Логи живут на ноде только у существующих подов: удалённый под — ошибка NotFound.
func (s *Service) PodLogs(ctx context.Context, namespace, pod, container string, since time.Time, tail int64, previous bool) ([]k8sModel.LogLine, error) {
	if s.initErr != nil {
		return nil, s.initErr
	}

	opts := &corev1.PodLogOptions{
		Container:  container,
		Timestamps: true,
		Previous:   previous,
		TailLines:  &tail,
		LimitBytes: new(int64(podLogsLimitBytes)),
	}
	if !since.IsZero() {
		opts.SinceTime = new(metav1.NewTime(since))
	}

	stream, err := s.clientset.CoreV1().Pods(namespace).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("Pods.GetLogs(%s/%s/%s): %w", namespace, pod, container, err)
	}
	defer func() { _ = stream.Close() }()

	lines := make([]k8sModel.LogLine, 0, 64)
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		ts, text, ok := strings.Cut(scanner.Text(), " ")
		at, err := time.Parse(time.RFC3339Nano, ts)
		if !ok || err != nil {
			continue
		}
		lines = append(lines, k8sModel.LogLine{TS: at, Text: text})
	}
	if err := scanner.Err(); err != nil && len(lines) == 0 {
		return nil, fmt.Errorf("read logs %s/%s/%s: %w", namespace, pod, container, err)
	}

	return lines, nil
}
