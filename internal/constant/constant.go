package constant

const (
	ServiceName = "pulse"

	MaxPageSize = 1000
)

// Version, Commit, BuiltAt подставляются при сборке: -ldflags "-X .../internal/constant.Version=<ver>"
// (Commit — полный SHA: манифест сервиса, build.commit).
var (
	Version = "dev"
	Commit  = ""
	BuiltAt = ""
)

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
	// SourceServiceStatus — ручка состояния самого сервиса (манифест)
	SourceServiceStatus = "service_status"
	SourceRuto          = "ruto"
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
	// WorkloadKindJob — Job'ы, которые оркестратор создаёт сам: один workload на семейство
	// (имя — общий префикс имён Job'ов), для образа, у которого нет своего workload'а
	WorkloadKindJob = "Job"
)

// критичность сервиса
const (
	CriticalityHigh   = "high"
	CriticalityMedium = "medium"
	CriticalityLow    = "low"
)

// откуда взято совпадение в resolve_service
const (
	MatchedByName  = "name"
	MatchedByAlias = "alias"
	MatchedByTitle = "title"
	// MatchedByClusterName — имя workload'а, k8s Service или приложения ruto
	MatchedByClusterName = "cluster_name"
	MatchedByFuzzy       = "fuzzy"
	MatchedByDescription = "description"
	// MatchedByTranslit — совпал латинский вариант кириллического запроса («караван» → caravan)
	MatchedByTranslit = "translit"
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

// причины проблемных состояний подов, вычисляемые сервисом (остальные приходят из kubernetes)
const (
	// PodProblemRestarting — контейнер работает, но перезапускался внутри окна
	PodProblemRestarting = "Restarting"
)

// направление метрики
const (
	MetricDirectionHigherIsBetter = "higher_is_better"
	MetricDirectionLowerIsBetter  = "lower_is_better"
)
