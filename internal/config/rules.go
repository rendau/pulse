package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

// Rules — структурные правила из yaml-файла (RULES_PATH). Всё, что неудобно
// выражать плоскими env: маппинг образов на репозитории, исключения namespace.
type Rules struct {
	Indexer struct {
		// namespace'ы, которые индексер пропускает
		ExcludeNamespaces []string `yaml:"exclude_namespaces"`
		// workload, не видевшийся дольше этого срока, удаляется из каталога
		StaleAfter time.Duration `yaml:"stale_after"`
	} `yaml:"indexer"`

	// правила «имя образа → репозиторий GitHub», применяются по порядку, первое совпадение.
	// Образ, не подошедший ни под одно правило, считается сторонним (postgres, redis…):
	// его workload попадает в каталог без репозитория.
	ImageMapping []ImageMapping `yaml:"image_mapping"`
}

// ImageMapping — правило маппинга образа на репозиторий.
//
// Плейсхолдеры в repo_template: {path} — путь образа без registry
// (mechta-market/promo-sync), {org} — первый сегмент пути (или поле org),
// {image_name} — последний сегмент пути.
type ImageMapping struct {
	Registry     string `yaml:"registry"`
	RepoTemplate string `yaml:"repo_template"`
	Org          string `yaml:"org"`
}

// LoadRules читает yaml по пути. Отсутствующий файл — не ошибка: возвращаются дефолты.
func LoadRules(path string) (*Rules, error) {
	rules := defaultRules()

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return rules, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if err = yaml.Unmarshal(raw, rules); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if rules.Indexer.StaleAfter <= 0 {
		rules.Indexer.StaleAfter = time.Hour
	}

	return rules, nil
}

func defaultRules() *Rules {
	rules := &Rules{}
	rules.Indexer.ExcludeNamespaces = []string{"kube-system", "kube-public", "kube-node-lease"}
	rules.Indexer.StaleAfter = time.Hour
	rules.ImageMapping = []ImageMapping{
		{Registry: "ghcr.io", RepoTemplate: "https://github.com/{path}"},
	}
	return rules
}
