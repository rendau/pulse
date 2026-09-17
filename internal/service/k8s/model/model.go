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
	// ConfigRefs — имена configmap/secret, подключённых к шаблону пода (envFrom, env valueFrom,
	// volumes); только имена, значения не читаются
	ConfigRefs []string
	CreatedAt  time.Time
}

// Container — контейнер из спеки шаблона пода (init-контейнеры не включаются).
type Container struct {
	Name  string
	Image string
	Env   []EnvVar
	// EnvFromConfigMaps — имена configmap'ов из envFrom (значения читаются отдельно)
	EnvFromConfigMaps []string
}

// EnvVar — переменная окружения контейнера. Значение из secret никогда не читается:
// у такой переменной Value пустой и FromSecret=true.
type EnvVar struct {
	Name  string
	Value string
	// ConfigMapRef — «name/key», если значение берётся из configmap
	ConfigMapRef string
	FromSecret   bool
}

// ConfigMap — данные configmap'а (не секрет).
type ConfigMap struct {
	Namespace string
	Name      string
	Data      map[string]string
}

// Service — k8s Service: имя хоста внутри кластера и селектор подов.
type Service struct {
	Namespace string
	Name      string
	Selector  map[string]string
	ClusterIP string
	Ports     []ServicePort
}

type ServicePort struct {
	Name       string
	Port       int32
	TargetPort string
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

// ReplicaSet — ревизия шаблона пода Deployment'а. Аннотации шаблона показывают, чем вызвана
// выкатка: keel (новый образ), reloader (смена configmap/secret), kubectl rollout restart.
type ReplicaSet struct {
	Namespace           string
	Name                string
	OwnerKind           string
	OwnerName           string
	Revision            int64
	CreatedAt           time.Time
	TemplateAnnotations map[string]string
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

// Node — нода кластера.
type Node struct {
	Name          string
	Ready         bool
	Unschedulable bool
	// Pressures — активные условия давления: MemoryPressure, DiskPressure, PIDPressure, NetworkUnavailable
	Pressures      []string
	KubeletVersion string
	Roles          []string
	CPUMillis      int64 // allocatable
	MemoryBytes    int64 // allocatable
	CreatedAt      time.Time
}
