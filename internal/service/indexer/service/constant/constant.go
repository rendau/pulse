package constant

const (
	// ServiceYamlPath — файл метаданных в корне репозитория сервиса
	ServiceYamlPath = "service.yaml"
	// RevisionLabel — OCI-label с SHA коммита, из которого собран образ
	RevisionLabel = "org.opencontainers.image.revision"
	// GhcrHost — registry, у которого коммит сборки ищется через пакеты и запуски GitHub Actions
	GhcrHost = "ghcr.io"

	GithubConcurrency   = 5
	RegistryConcurrency = 5
)
