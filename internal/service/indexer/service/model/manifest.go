package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	"github.com/mechta-market/pulse/internal/util/redact"
)

// Манифест сервиса (docs/service-manifest.md): транспортная модель и проверка по стандарту.

const (
	// ManifestVersion — версия стандарта; pulse понимает её и предыдущую
	ManifestVersion = 1
	// ManifestMaxBytes — манифест больше не принимается
	ManifestMaxBytes = 64 << 10

	maxEndpoints     = 30
	maxDependencies  = 30
	maxMetrics       = 20
	maxParams        = 10
	maxAliases       = 20
	maxSchemaDepth   = 6
	maxTitleChars    = 100
	maxTextChars     = 500
	defaultTimeoutMs = 5000
	maxTimeoutMs     = 10000
	defaultMaxRows   = 50
	maxMaxRows       = 100
)

var (
	serviceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	idRe          = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	paramNameRe   = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,40}$`)
	pathParamRe   = regexp.MustCompile(`\{([^{}]*)\}`)
	commitRe      = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

	criticalities   = []string{"high", "medium", "low"}
	dependencyKinds = []string{"postgres", "redis", "kafka", "rabbitmq", "clickhouse", "elasticsearch", "s3", "http", "grpc", "other"}
	schemaTypes     = []string{"object", "array", "string", "integer", "number", "boolean"}
	paramTypes      = []string{"string", "integer", "number", "boolean"}
	metricUnits     = []string{"", "count", "ratio", "seconds", "bytes", "rps"}
	directions      = []string{"", "higher_is_better", "lower_is_better"}

	// PersonalKinds — виды персональных данных (x-personal)
	PersonalKinds = []string{"phone", "email", "iin", "customer_id", "name", "address", "document", "card", "other"}
)

// Manifest — ответ GET /.well-known/pulse.
type Manifest struct {
	PulseManifest int                  `json:"pulse_manifest"`
	Service       ManifestService      `json:"service"`
	Build         ManifestBuild        `json:"build"`
	Runbooks      []ManifestRunbook    `json:"runbooks"`
	Dependencies  []ManifestDependency `json:"dependencies"`
	Metrics       []ManifestMetric     `json:"metrics"`
	Logs          ManifestLogs         `json:"logs"`
	Endpoints     []ManifestEndpoint   `json:"endpoints"`
}

type ManifestService struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
	Owner       struct {
		Team     string   `json:"team"`
		Contacts []string `json:"contacts"`
	} `json:"owner"`
	Criticality string `json:"criticality"`
	RepoUrl     string `json:"repo_url"`
	DocsUrl     string `json:"docs_url"`
}

type ManifestBuild struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at"`
}

type ManifestRunbook struct {
	Title string `json:"title"`
	Url   string `json:"url"`
}

type ManifestDependency struct {
	Id       string `json:"id"`
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Critical bool   `json:"critical"`
}

type ManifestMetric struct {
	Id        string `json:"id"`
	Title     string `json:"title"`
	PromQL    string `json:"promql"`
	Unit      string `json:"unit"`
	Direction string `json:"direction"`
}

type ManifestLogs struct {
	Selector      string `json:"selector"`
	ErrorPatterns []struct {
		Name    string `json:"name"`
		Pattern string `json:"pattern"`
	} `json:"error_patterns"`
}

type ManifestEndpoint struct {
	Id          string                   `json:"id"`
	Title       string                   `json:"title"`
	Description string                   `json:"description"`
	Path        string                   `json:"path"`
	Params      map[string]ManifestParam `json:"params"`
	TimeoutMs   int                      `json:"timeout_ms"`
	Response    *ManifestSchema          `json:"response"`
	RowsPath    string                   `json:"rows_path"`
	MaxRows     int                      `json:"max_rows"`
}

type ManifestParam struct {
	Type        string          `json:"type"`
	Pattern     string          `json:"pattern"`
	Enum        []any           `json:"enum"`
	Min         *float64        `json:"min"`
	Max         *float64        `json:"max"`
	Default     any             `json:"default"`
	Required    bool            `json:"required"`
	Description string          `json:"description"`
	Personal    json.RawMessage `json:"x-personal"`
}

// ManifestSchema — подмножество JSON Schema. Неподдерживаемые ключевые слова разбираются,
// только чтобы отказать: схема должна однозначно говорить, какие поля есть.
type ManifestSchema struct {
	Type                 string                     `json:"type"`
	Properties           map[string]*ManifestSchema `json:"properties"`
	Items                *ManifestSchema            `json:"items"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	Enum                 []any                      `json:"enum"`
	Format               string                     `json:"format"`
	MaxLength            int                        `json:"maxLength"`
	MaxItems             int                        `json:"maxItems"`
	Description          string                     `json:"description"`
	Personal             json.RawMessage            `json:"x-personal"`

	Ref               string          `json:"$ref"`
	OneOf             json.RawMessage `json:"oneOf"`
	AnyOf             json.RawMessage `json:"anyOf"`
	AllOf             json.RawMessage `json:"allOf"`
	PatternProperties json.RawMessage `json:"patternProperties"`
}

// ParsedManifest — принятый манифест: метаданные каталога и то, что отклонено (Problems —
// причины partial). Endpoints ещё не знают свой workload — его проставляет индексер.
type ParsedManifest struct {
	Name          string
	Title         string
	Description   string
	Aliases       []string
	OwnerTeam     string
	OwnerContacts []string
	Criticality   string
	RepoUrl       string
	Commit        string
	Metadata      svcModel.Metadata
	Problems      []string
}

// IsManifest — ответ похож на манифест: JSON-объект с полем pulse_manifest. Иначе это не
// манифест, а ответ сервера на любой путь (SPA, catch-all отдают 200 и страницу или {}).
func IsManifest(raw []byte) bool {
	var probe struct {
		PulseManifest json.RawMessage `json:"pulse_manifest"`
	}
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{' && json.Unmarshal(raw, &probe) == nil && len(probe.PulseManifest) > 0
}

// LooksLikeManifest — начало ответа (обрезанного по лимиту) похоже на манифест: JSON-объект,
// где встречается pulse_manifest. Большая страница на любой путь — не манифест.
func LooksLikeManifest(prefix []byte) bool {
	prefix = bytes.TrimSpace(prefix)
	return len(prefix) > 0 && prefix[0] == '{' && bytes.Contains(prefix, []byte(`"pulse_manifest"`))
}

// ParseManifest разбирает и проверяет манифест. Ошибка — манифест не принят целиком
// (invalid); Problems — принят, но часть отклонена (partial).
func ParseManifest(raw []byte) (*ParsedManifest, error) {
	if len(raw) > ManifestMaxBytes {
		return nil, fmt.Errorf("манифест больше %d KB", ManifestMaxBytes>>10)
	}
	m := &Manifest{}
	if err := json.Unmarshal(raw, m); err != nil {
		return nil, fmt.Errorf("не JSON манифеста: %w", err)
	}

	switch {
	case m.PulseManifest == 0:
		return nil, fmt.Errorf("нет pulse_manifest (версии стандарта)")
	case m.PulseManifest > ManifestVersion || m.PulseManifest < ManifestVersion-1:
		return nil, fmt.Errorf("pulse_manifest %d: pulse понимает версию %d", m.PulseManifest, ManifestVersion)
	}

	svc := m.Service
	switch {
	case !serviceNameRe.MatchString(svc.Name):
		return nil, fmt.Errorf("service.name %q: ожидается %s", svc.Name, serviceNameRe)
	case strings.TrimSpace(svc.Title) == "":
		return nil, fmt.Errorf("нет service.title")
	case strings.TrimSpace(svc.Description) == "":
		return nil, fmt.Errorf("нет service.description")
	case strings.TrimSpace(svc.Owner.Team) == "":
		return nil, fmt.Errorf("нет service.owner.team")
	case !slices.Contains(criticalities, svc.Criticality):
		return nil, fmt.Errorf("service.criticality %q: ожидается high, medium или low", svc.Criticality)
	case len(m.Endpoints) > maxEndpoints:
		return nil, fmt.Errorf("ручек %d, максимум %d", len(m.Endpoints), maxEndpoints)
	case len(m.Dependencies) > maxDependencies:
		return nil, fmt.Errorf("зависимостей %d, максимум %d", len(m.Dependencies), maxDependencies)
	case len(m.Metrics) > maxMetrics:
		return nil, fmt.Errorf("метрик %d, максимум %d", len(m.Metrics), maxMetrics)
	}

	result := &ParsedManifest{
		Name:        svc.Name,
		Title:       clip(svc.Title, maxTitleChars),
		Description: clip(svc.Description, maxTextChars),
		Aliases:     lo.Slice(lo.Uniq(lo.Compact(lo.Map(svc.Aliases, func(a string, _ int) string { return strings.TrimSpace(a) }))), 0, maxAliases),
		OwnerTeam:   strings.TrimSpace(svc.Owner.Team),
		Criticality: svc.Criticality,
		RepoUrl:     svc.RepoUrl,
		Metadata: svcModel.Metadata{
			Source:  svcModel.MetadataSourceManifest,
			DocsUrl: httpUrl(svc.DocsUrl),
			Logs:    svcModel.Logs{Selector: m.Logs.Selector},
		},
	}
	problem := func(format string, args ...any) {
		result.Problems = append(result.Problems, fmt.Sprintf(format, args...))
	}
	result.Problems = append(result.Problems, longTexts(m)...)

	// контакты — чат или почта команды; личные телефоны не принимаются
	for _, c := range lo.Compact(svc.Owner.Contacts) {
		if redact.HasPII(c) && !strings.Contains(c, "@") || phoneLike(c) {
			problem("owner.contacts: контакт похож на телефон — не принят (нужны чат или почта команды)")
			continue
		}
		result.OwnerContacts = append(result.OwnerContacts, c)
	}

	switch commit := strings.ToLower(strings.TrimSpace(m.Build.Commit)); {
	case commit == "":
	case commitRe.MatchString(commit):
		result.Commit = commit
	default:
		problem("build.commit %q: ожидается SHA коммита", m.Build.Commit)
	}

	for _, r := range m.Runbooks {
		if u := httpUrl(r.Url); u != "" && strings.TrimSpace(r.Title) != "" {
			result.Metadata.Runbooks = append(result.Metadata.Runbooks, svcModel.Runbook{Title: clip(r.Title, maxTitleChars), Url: u})
		} else {
			problem("runbooks: нужны title и http(s)-ссылка (%q)", r.Title)
		}
	}

	seenDeps := map[string]bool{}
	for _, d := range m.Dependencies {
		switch {
		case !idRe.MatchString(d.Id) || seenDeps[d.Id]:
			problem("dependencies: id %q неверный или повторяется", d.Id)
		case !slices.Contains(dependencyKinds, d.Kind):
			problem("dependencies.%s: kind %q не из списка стандарта", d.Id, d.Kind)
		case strings.TrimSpace(d.Target) == "":
			problem("dependencies.%s: нет target", d.Id)
		case strings.Contains(d.Target, "@") || redact.Userinfo(d.Target) != d.Target:
			problem("dependencies.%s: в target учётные данные — нужен только хост", d.Id)
		default:
			seenDeps[d.Id] = true
			result.Metadata.Dependencies = append(result.Metadata.Dependencies, svcModel.Dependency{
				Id: d.Id, Kind: d.Kind, Target: strings.TrimSpace(d.Target), Critical: d.Critical,
			})
		}
	}

	for _, mt := range m.Metrics {
		switch {
		case !idRe.MatchString(mt.Id) || strings.TrimSpace(mt.PromQL) == "":
			problem("metrics: нужны id (%q) и promql", mt.Id)
		case !slices.Contains(metricUnits, mt.Unit) || !slices.Contains(directions, mt.Direction):
			problem("metrics.%s: unit или direction не из списка стандарта", mt.Id)
		default:
			result.Metadata.Metrics = append(result.Metadata.Metrics, svcModel.Metric{
				Id: mt.Id, Title: clip(mt.Title, maxTitleChars), PromQL: mt.PromQL, Unit: mt.Unit, Direction: mt.Direction,
			})
		}
	}

	for _, p := range m.Logs.ErrorPatterns {
		if _, err := regexp.Compile(p.Pattern); err != nil || p.Pattern == "" || p.Name == "" {
			problem("logs.error_patterns: %q — нужны name и регэксп RE2", p.Name)
			continue
		}
		result.Metadata.Logs.ErrorPatterns = append(result.Metadata.Logs.ErrorPatterns, svcModel.ErrorPattern{Name: p.Name, Pattern: p.Pattern})
	}

	seenEndpoints := map[string]bool{}
	for _, e := range m.Endpoints {
		endpoint, err := parseEndpoint(e)
		switch {
		case err != nil:
			problem("endpoints.%s: %s", e.Id, err)
		case seenEndpoints[e.Id]:
			problem("endpoints.%s: id повторяется", e.Id)
		default:
			seenEndpoints[e.Id] = true
			result.Metadata.Endpoints = append(result.Metadata.Endpoints, *endpoint)
		}
	}

	return result, nil
}

func parseEndpoint(e ManifestEndpoint) (*svcModel.Endpoint, error) {
	switch {
	case !idRe.MatchString(e.Id):
		return nil, fmt.Errorf("id: ожидается %s", idRe)
	case strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.Description) == "":
		return nil, fmt.Errorf("нужны title и description (description — для агента: когда вызывать)")
	case !strings.HasPrefix(e.Path, "/") || strings.Contains(e.Path, "..") || strings.ContainsAny(e.Path, "?#"):
		return nil, fmt.Errorf("path %q: абсолютный путь без .. и query", e.Path)
	case len(e.Params) > maxParams:
		return nil, fmt.Errorf("параметров %d, максимум %d", len(e.Params), maxParams)
	case e.Response == nil:
		return nil, fmt.Errorf("нет схемы ответа (response): без неё pulse ничего не пропустит")
	}

	endpoint := &svcModel.Endpoint{
		Id:          e.Id,
		Title:       clip(e.Title, maxTitleChars),
		Description: clip(e.Description, maxTextChars),
		Path:        e.Path,
		Params:      make(map[string]svcModel.EndpointParam, len(e.Params)),
		RowsPath:    e.RowsPath,
		MaxRows:     lo.Clamp(lo.CoalesceOrEmpty(e.MaxRows, defaultMaxRows), 1, maxMaxRows),
		Timeout:     time.Duration(lo.Clamp(lo.CoalesceOrEmpty(e.TimeoutMs, defaultTimeoutMs), 100, maxTimeoutMs)) * time.Millisecond,
	}

	for _, m := range pathParamRe.FindAllStringSubmatch(e.Path, -1) {
		if _, ok := e.Params[m[1]]; !ok {
			return nil, fmt.Errorf("параметр пути {%s} не объявлен в params", m[1])
		}
	}
	for name, p := range e.Params {
		param, err := parseParam(name, p)
		if err != nil {
			return nil, fmt.Errorf("params.%s: %w", name, err)
		}
		if strings.Contains(e.Path, "{"+name+"}") {
			param.Required = true
		}
		endpoint.Params[name] = *param
	}

	schema, err := parseSchema(e.Response, "response", 1)
	if err != nil {
		return nil, err
	}
	endpoint.Response = schema

	if e.RowsPath != "" {
		rows := schema
		for _, key := range strings.Split(e.RowsPath, ".") {
			if rows == nil || rows.Type != "object" {
				rows = nil
				break
			}
			rows = rows.Properties[key]
		}
		if rows == nil || rows.Type != "array" {
			return nil, fmt.Errorf("rows_path %q не указывает на массив в схеме ответа", e.RowsPath)
		}
	}

	return endpoint, nil
}

func parseParam(name string, p ManifestParam) (*svcModel.EndpointParam, error) {
	personal, err := parsePersonal(p.Personal)
	switch {
	case err != nil:
		return nil, err
	case !paramNameRe.MatchString(name):
		return nil, fmt.Errorf("имя: ожидается %s", paramNameRe)
	case redact.SecretName(name):
		return nil, fmt.Errorf("имя похоже на секрет — такие параметры запрещены")
	case !slices.Contains(paramTypes, lo.CoalesceOrEmpty(p.Type, "string")):
		return nil, fmt.Errorf("type %q: ожидается string, integer, number или boolean", p.Type)
	}

	param := &svcModel.EndpointParam{
		Type:        lo.CoalesceOrEmpty(p.Type, "string"),
		Min:         p.Min,
		Max:         p.Max,
		Required:    p.Required,
		Pattern:     p.Pattern,
		Enum:        lo.Map(p.Enum, func(v any, _ int) string { return fmt.Sprint(v) }),
		Description: clip(p.Description, maxTextChars),
		Personal:    personal,
	}
	if p.Default != nil {
		param.Default = fmt.Sprint(p.Default)
	}

	if p.Pattern != "" {
		if _, err := regexp.Compile(p.Pattern); err != nil {
			return nil, fmt.Errorf("pattern: не регэксп RE2: %w", err)
		}
	}
	if param.Type == "string" && p.Pattern == "" && len(p.Enum) == 0 && personal == "" {
		return nil, fmt.Errorf("строковому параметру нужен pattern, enum или x-personal — свободная строка запрещена")
	}
	if personal != "" && param.Type != "string" {
		return nil, fmt.Errorf("x-personal — только у строковых параметров")
	}
	if personal == "card" {
		return nil, fmt.Errorf("x-personal card: номер карты не токенизируется — по нему искать нельзя")
	}

	return param, nil
}

func parseSchema(s *ManifestSchema, path string, depth int) (*svcModel.Schema, error) {
	switch {
	case s == nil:
		return nil, fmt.Errorf("%s: пустая схема", path)
	case depth > maxSchemaDepth:
		return nil, fmt.Errorf("%s: вложенность схемы больше %d", path, maxSchemaDepth)
	case s.Ref != "" || len(s.OneOf) > 0 || len(s.AnyOf) > 0 || len(s.AllOf) > 0 || len(s.PatternProperties) > 0:
		return nil, fmt.Errorf("%s: $ref, oneOf, anyOf, allOf, patternProperties не поддерживаются", path)
	case !slices.Contains(schemaTypes, s.Type):
		return nil, fmt.Errorf("%s: type %q не из %s", path, s.Type, strings.Join(schemaTypes, ", "))
	}

	personal, err := parsePersonal(s.Personal)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if personal != "" && s.Type != "string" && s.Type != "integer" {
		return nil, fmt.Errorf("%s: x-personal — только у строк и целых чисел", path)
	}

	result := &svcModel.Schema{
		Type:        s.Type,
		Enum:        lo.Map(s.Enum, func(v any, _ int) string { return fmt.Sprint(v) }),
		Format:      s.Format,
		MaxLength:   max(s.MaxLength, 0),
		MaxItems:    max(s.MaxItems, 0),
		Description: clip(s.Description, maxTextChars),
		Personal:    personal,
	}

	switch s.Type {
	case "object":
		if len(s.Properties) > 0 {
			result.Properties = make(map[string]*svcModel.Schema, len(s.Properties))
		}
		for name, prop := range s.Properties {
			if redact.SecretName(name) {
				return nil, fmt.Errorf("%s.%s: имя поля похоже на секрет — такие поля запрещены (переименуйте, если это не секрет)", path, name)
			}
			child, err := parseSchema(prop, path+"."+name, depth+1)
			if err != nil {
				return nil, err
			}
			result.Properties[name] = child
		}
		values, err := parseAdditional(s.AdditionalProperties, path, depth)
		if err != nil {
			return nil, err
		}
		result.Values = values
	case "array":
		if s.Items == nil {
			return nil, fmt.Errorf("%s: у массива нет items", path)
		}
		items, err := parseSchema(s.Items, path+"[]", depth+1)
		if err != nil {
			return nil, err
		}
		result.Items = items
	}

	return result, nil
}

// parseAdditional — additionalProperties: словарь допустим только с числами или булевыми
// значениями (ключи проходят маскировку как текст); true/false — словаря нет.
func parseAdditional(raw json.RawMessage, path string, depth int) (*svcModel.Schema, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, nil
	}
	values := &ManifestSchema{}
	if err := json.Unmarshal(raw, values); err != nil {
		return nil, fmt.Errorf("%s.additionalProperties: %w", path, err)
	}
	if values.Type != "number" && values.Type != "integer" && values.Type != "boolean" {
		return nil, fmt.Errorf("%s.additionalProperties: словарь — только с number, integer или boolean", path)
	}
	return parseSchema(values, path+"{}", depth+1)
}

// parsePersonal — x-personal: вид персональных данных; true — other.
func parsePersonal(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0 || string(raw) == "false" || string(raw) == "null":
		return "", nil
	case string(raw) == "true":
		return "other", nil
	}
	var kind string
	if err := json.Unmarshal(raw, &kind); err != nil || !slices.Contains(PersonalKinds, kind) {
		return "", fmt.Errorf("x-personal %s: ожидается один из %s", raw, strings.Join(PersonalKinds, ", "))
	}
	return kind, nil
}

// longTexts — тексты длиннее лимитов стандарта: pulse их обрезает, владелец должен об этом знать
// (хвост описания для агента бывает самым важным).
func longTexts(m *Manifest) []string {
	var result []string
	check := func(what, text string, limit int) {
		if n := len([]rune(strings.TrimSpace(text))); n > limit {
			result = append(result, fmt.Sprintf("%s: %d символов, лимит %d — обрезано", what, n, limit))
		}
	}
	check("service.title", m.Service.Title, maxTitleChars)
	check("service.description", m.Service.Description, maxTextChars)
	for _, r := range m.Runbooks {
		check("runbooks.title", r.Title, maxTitleChars)
	}
	for _, mt := range m.Metrics {
		check("metrics."+mt.Id+".title", mt.Title, maxTitleChars)
	}
	var walk func(s *ManifestSchema, path string, depth int)
	walk = func(s *ManifestSchema, path string, depth int) {
		if s == nil || depth > maxSchemaDepth {
			return
		}
		check(path+".description", s.Description, maxTextChars)
		for name, prop := range s.Properties {
			walk(prop, path+"."+name, depth+1)
		}
		walk(s.Items, path+"[]", depth+1)
	}
	for _, e := range m.Endpoints {
		check("endpoints."+e.Id+".title", e.Title, maxTitleChars)
		check("endpoints."+e.Id+".description", e.Description, maxTextChars)
		for name, p := range e.Params {
			check("endpoints."+e.Id+".params."+name+".description", p.Description, maxTextChars)
		}
		walk(e.Response, "endpoints."+e.Id+".response", 1)
	}
	sort.Strings(result)
	return result
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func httpUrl(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}

var phoneLikeRe = regexp.MustCompile(`^\+?[\d\s()-]{10,}$`)

func phoneLike(s string) bool {
	return phoneLikeRe.MatchString(strings.TrimSpace(s))
}
