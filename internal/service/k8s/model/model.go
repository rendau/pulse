package model

import "time"

// Workload — контроллер в кластере: Deployment / StatefulSet / DaemonSet / CronJob.
type Workload struct {
	Kind            string
	Namespace       string
	Name            string
	ReplicasDesired int32
	// Selector — label-селектор подов workload'а («app=x,tier=y»); у CronJob пустой.
	Selector   string
	Containers []Container
	CreatedAt  time.Time
}

// Container — контейнер из спеки шаблона пода (init-контейнеры не включаются).
type Container struct {
	Name  string
	Image string
}

// Pod — состояние пода в кластере.
type Pod struct {
	Namespace  string
	Name       string
	Phase      string
	Ready      bool
	Restarts   int32
	StartedAt  time.Time
	NodeName   string
	Labels     map[string]string
	Containers []PodContainer
}

// PodContainer — статус контейнера внутри пода.
type PodContainer struct {
	Name     string
	Image    string
	ImageID  string // registry/path@sha256:… — то, что реально запущено
	Ready    bool
	Restarts int32
	// State — running | waiting | terminated
	State string
	// Reason — причина текущего waiting/terminated (CrashLoopBackOff, ImagePullBackOff…)
	Reason string
	// LastTerminationReason — причина последнего завершения (OOMKilled, Error…)
	LastTerminationReason string
	LastTerminatedAt      time.Time
}

// Event — событие кластера, привязанное к объекту (Pod, ReplicaSet, Deployment…).
type Event struct {
	Namespace  string
	ObjectKind string
	ObjectName string
	Reason     string
	// Type — Normal | Warning
	Type    string
	Message string
	Count   int32
	FirstTS time.Time
	LastTS  time.Time
}
