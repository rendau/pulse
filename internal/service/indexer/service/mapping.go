package service

import (
	"path"
	"strings"

	indexerModel "github.com/rendau/pulse/internal/service/indexer/model"
	"github.com/rendau/pulse/internal/util/imageref"
)

// imageMapper применяет правила «образ → репозиторий»: первое совпадение по registry
// и маске пути (если задана).
type imageMapper struct {
	rules []indexerModel.ImageMapping
}

func newImageMapper(rules []indexerModel.ImageMapping) *imageMapper {
	return &imageMapper{rules: rules}
}

// RepoUrl возвращает URL репозитория для образа; ok=false — образ сторонний.
func (m *imageMapper) RepoUrl(ref imageref.Ref) (string, bool) {
	for _, rule := range m.rules {
		if !strings.EqualFold(rule.Registry, ref.Host) {
			continue
		}
		if rule.Path != "" {
			// некорректная маска — правило не совпадает (валидность маски проверяется при загрузке правил)
			if matched, err := path.Match(rule.Path, ref.Path); err != nil || !matched {
				continue
			}
		}

		org := rule.Org
		if org == "" {
			org = ref.Org()
		}

		replacer := strings.NewReplacer(
			"{path}", ref.Path,
			"{repo}", ref.Repo(),
			"{org}", org,
			"{image_name}", ref.Name(),
		)
		repoUrl := replacer.Replace(rule.RepoTemplate)
		if !strings.Contains(repoUrl, "://") {
			repoUrl = "https://" + repoUrl
		}

		return repoUrl, true
	}

	return "", false
}
