package podname

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/constant"
)

// соседи сервиса pulse в одном namespace: префикс «pulse-» захватывал их всех
var pulseNeighbours = []string{
	"pulse-agent-6dcbf648c-zc5hq", "pulse-bot-565d847f94-7smwm", "pulse-pg-0",
	"pulse-agent-h7spq", "pulse-bot-0", "pulse-migrate-29311520-kd5ck",
}

func TestPattern(t *testing.T) {
	tests := []struct {
		kind, name string
		own        []string
		foreign    []string
	}{
		{
			kind: constant.WorkloadKindDeployment, name: "pulse",
			own:     []string{"pulse-6dcbf648c-zc5hq", "pulse-565d847f94-7smwm", "pulse-ff58847-vf8nq"},
			foreign: append([]string{"pulse", "pulse-6dcbf648c", "pulse-0", "pulse-h7spq", "pulse-watcher-h7spq", "pulse-6dcbf648c-zc5hq-x"}, pulseNeighbours...),
		},
		{
			kind: constant.WorkloadKindStatefulSet, name: "pulse-pg",
			own:     []string{"pulse-pg-0", "pulse-pg-12"},
			foreign: []string{"pulse-pg", "pulse-pg-backup-0", "pulse-pg-6dcbf648c-zc5hq", "pulse-pg-0-x"},
		},
		{
			kind: constant.WorkloadKindDaemonSet, name: "traefik",
			own:     []string{"traefik-h7spq"},
			foreign: []string{"traefik-mesh-h7spq", "traefik-6dcbf648c-zc5hq", "traefik-0", "traefik-abcde"},
		},
		{
			kind: constant.WorkloadKindCronJob, name: "pulse-report",
			own:     []string{"pulse-report-29311520-kd5ck"},
			foreign: []string{"pulse-report-29311520", "pulse-report-weekly-29311520-kd5ck", "pulse-report-6dcbf648c-zc5hq"},
		},
		{
			// семейство Job'ов оркестратора: имя — общий префикс, поды — любые «имя-…»
			kind: constant.WorkloadKindJob, name: "nightly",
			own:     []string{"nightly-product-sync-1-9da3-x2k4p", "nightly-delivery-q2"},
			foreign: []string{"nightly", "nightlyx-q2", "other-nightly-q2"},
		},
		{
			// точка в имени — литерал
			kind: constant.WorkloadKindStatefulSet, name: "a.b",
			own:     []string{"a.b-0"},
			foreign: []string{"axb-0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.kind+"/"+tt.name, func(t *testing.T) {
			for _, pod := range tt.own {
				assert.True(t, Match(tt.kind, tt.name, pod), pod)
			}
			for _, pod := range tt.foreign {
				assert.False(t, Match(tt.kind, tt.name, pod), pod)
			}
		})
	}
}

// длинное имя: основа generateName обрезается до 58 символов вместе с хэшем
func TestPatternTruncated(t *testing.T) {
	name := strings.Repeat("a", 45) + "-service" // 53
	hash, rnd := "565d847f94", "7smwm"
	pod := (name + "-" + hash + "-")[:maxGenerated] + rnd
	assert.True(t, Match(constant.WorkloadKindDeployment, name, pod), pod)
	assert.False(t, Match(constant.WorkloadKindDeployment, name, name+"-x-"+rnd))

	// имя 48 символов: хэш до 8 знаков влезает с «-», длиннее — обрезан до 9
	name = strings.Repeat("b", 48)
	for _, h := range []string{"6dcbf6", "6dcbf648c", "565d847f94"} {
		pod := (name + "-" + h + "-")
		pod = pod[:min(len(pod), maxGenerated)] + rnd
		assert.True(t, Match(constant.WorkloadKindDeployment, name, pod), pod)
	}

	// основа длиннее 58 — только обрезанное имя и суффикс
	name = strings.Repeat("c", 60)
	assert.True(t, Match(constant.WorkloadKindDaemonSet, name, name[:maxGenerated]+rnd))
	assert.True(t, Match(constant.WorkloadKindDeployment, name, name[:maxGenerated]+rnd))

	// CronJob: имя до 52 символов, Job «имя-<8 цифр>»
	name = strings.Repeat("d", 52)
	pod = (name + "-29311520-")[:maxGenerated] + rnd
	assert.True(t, Match(constant.WorkloadKindCronJob, name, pod), pod)
}

func TestObjectPattern(t *testing.T) {
	for _, object := range []string{"pulse", "pulse-6dcbf648c", "pulse-6dcbf648c-zc5hq"} {
		assert.True(t, MatchObject(constant.WorkloadKindDeployment, "pulse", object), object)
	}
	for _, object := range append([]string{"pulse-agent"}, pulseNeighbours...) {
		assert.False(t, MatchObject(constant.WorkloadKindDeployment, "pulse", object), object)
	}
	for _, object := range []string{"pulse-report", "pulse-report-29311520", "pulse-report-29311520-kd5ck"} {
		assert.True(t, MatchObject(constant.WorkloadKindCronJob, "pulse-report", object), object)
	}
	assert.True(t, MatchObject(constant.WorkloadKindStatefulSet, "pulse-pg", "pulse-pg-0"))
	assert.False(t, MatchObject(constant.WorkloadKindStatefulSet, "pulse-pg", "pulse-pg-backup"))
}

func TestRegex(t *testing.T) {
	regex := Regex([]Workload{{constant.WorkloadKindDeployment, "pulse"}, {constant.WorkloadKindStatefulSet, "pulse-pg"}})
	assert.Equal(t, "^(?:pulse-"+alnum+"{6,10}-"+alnum+"{5}|pulse-pg-[0-9]+)$", regex)
	assert.NotContains(t, regex, `\`, "без обратных слешей: строка PromQL")

	re := regexp.MustCompile(regex)
	assert.True(t, re.MatchString("pulse-6dcbf648c-zc5hq"))
	assert.True(t, re.MatchString("pulse-pg-0"))
	assert.False(t, re.MatchString("pulse-agent-6dcbf648c-zc5hq"))

	assert.Equal(t, "^pulse-pg-[0-9]+$", Regex([]Workload{{constant.WorkloadKindStatefulSet, "pulse-pg"}, {constant.WorkloadKindStatefulSet, "pulse-pg"}}))
	require.NotPanics(t, func() { regexp.MustCompile(Regex([]Workload{{constant.WorkloadKindCronJob, strings.Repeat("x", 52)}})) })
}

func TestOwner(t *testing.T) {
	workloads := []Workload{
		{constant.WorkloadKindDeployment, "pulse"},
		{constant.WorkloadKindDeployment, "pulse-agent"},
		{constant.WorkloadKindStatefulSet, "pulse-pg"},
		{constant.WorkloadKindCronJob, "pulse-report"},
		{constant.WorkloadKindJob, "nightly"},
	}
	for pod, want := range map[string]int{
		"pulse-6dcbf648c-zc5hq":       0,
		"pulse-agent-6dcbf648c-zc5hq": 1,
		"pulse-pg-0":                  2,
		"pulse-report-29311520-kd5ck": 3,
		"pulse-report-manual-x2k4b":   3, // вне правил — самый длинный префикс
		"nightly-sync-1-9da3-x2k4p":   4,
		"other-6dcbf648c-zc5hq":       -1,
	} {
		assert.Equal(t, want, Owner(pod, workloads), pod)
	}

	assert.Equal(t, 1, ObjectOwner("pulse-agent", workloads))
	assert.Equal(t, 1, ObjectOwner("pulse-agent-6dcbf648c", workloads))
	assert.Equal(t, 0, ObjectOwner("pulse-6dcbf648c", workloads))
	assert.Equal(t, 3, ObjectOwner("pulse-report-29311520", workloads))
	assert.Equal(t, -1, ObjectOwner("other", workloads))
}
