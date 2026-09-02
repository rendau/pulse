package constant

const (
	// ServiceYamlPath — файл метаданных в корне репозитория сервиса
	ServiceYamlPath = "service.yaml"
	// RevisionLabel — OCI-label с SHA коммита, из которого собран образ
	RevisionLabel = "org.opencontainers.image.revision"

	GithubConcurrency   = 5
	RegistryConcurrency = 5
)
