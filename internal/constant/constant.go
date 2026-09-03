package constant

const (
	ServiceName = "pulse"

	MaxPageSize = 1000
)

// Version подставляется при сборке: -ldflags "-X .../internal/constant.Version=<ver>".
var Version = "dev"

// источники данных (Event.Source, статусы в ping)
const (
	SourcePostgres     = "postgres"
	SourceK8s          = "k8s"
	SourceGithub       = "github"
	SourceRegistry     = "registry"
	SourcePrometheus   = "prometheus"
	SourceLoki         = "loki"
	SourceAlertmanager = "alertmanager"
	SourceKusec        = "kusec"
	SourceRuto         = "ruto"
)

// статусы подключения к источнику (ping)
const (
	SourceStatusOk       = "ok"
	SourceStatusError    = "error"
	SourceStatusDisabled = "disabled"
)

// виды workload
const (
	WorkloadKindDeployment  = "Deployment"
	WorkloadKindStatefulSet = "StatefulSet"
	WorkloadKindDaemonSet   = "DaemonSet"
	WorkloadKindCronJob     = "CronJob"
)

// критичность сервиса
const (
	CriticalityHigh   = "high"
	CriticalityMedium = "medium"
	CriticalityLow    = "low"
)

// откуда взято совпадение в resolve_service
const (
	MatchedByName        = "name"
	MatchedByAlias       = "alias"
	MatchedByTitle       = "title"
	MatchedByFuzzy       = "fuzzy"
	MatchedByDescription = "description"
)

// типы нормализованных событий (Event.Type)
const (
	EventTypeDeploy       = "deploy"
	EventTypeRestart      = "restart"
	EventTypeOOMKill      = "oom_kill"
	EventTypeAlertFiring  = "alert_firing"
	EventTypeConfigChange = "config_change"
	EventTypeCommit       = "commit"
	EventTypeScale        = "scale"
	EventTypeWarning      = "warning"
	EventTypeInfo         = "info"
)

// severity событий
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// направление метрики
const (
	MetricDirectionHigherIsBetter = "higher_is_better"
	MetricDirectionLowerIsBetter  = "lower_is_better"
)
