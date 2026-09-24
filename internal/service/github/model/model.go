package model

import "time"

// Commit — коммит ветки по умолчанию.
type Commit struct {
	SHA     string
	Author  string // имя автора либо логин
	Login   string
	Message string // первая строка сообщения
	Date    time.Time
	Url     string
}

func (c Commit) ShortSHA() string {
	if len(c.SHA) > 7 {
		return c.SHA[:7]
	}
	return c.SHA
}

// Comparison — сравнение base (задеплоенный коммит) с head (ветка по умолчанию).
type Comparison struct {
	// AheadBy — сколько коммитов в head нет в base: «в проде отстаёт на N коммитов»
	AheadBy  int
	BehindBy int
	Commits  []Commit
}

// Repo — сведения о репозитории: ветка по умолчанию, описание и topics (по ним поиск
// находит сервисы без service.yaml: «платёжный шлюз» в описании → acquiring-broker).
type Repo struct {
	DefaultBranch string
	Description   string
	Topics        []string
}
