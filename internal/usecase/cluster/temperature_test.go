package cluster

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clusterModel "github.com/rendau/pulse/internal/domain/cluster/model"
	snapshotModel "github.com/rendau/pulse/internal/domain/snapshot/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	prometheusModel "github.com/rendau/pulse/internal/service/prometheus/model"
	prometheusService "github.com/rendau/pulse/internal/service/prometheus/service"
)

// homeSample — метки node-exporter kube-prometheus-stack, как на домашнем сервере (k3s).
func homeSample(name string, value float64, labels ...string) prometheusModel.Sample {
	l := map[string]string{"__name__": name, "job": "node-exporter", "instance": "192.168.1.30:9100", "pod": "prometheus-prometheus-node-exporter-58pkg"}
	for i := 0; i+1 < len(labels); i += 2 {
		l[labels[i]] = labels[i+1]
	}
	return prometheusModel.Sample{Labels: l, Value: value}
}

func homeSamples(cpu float64) []prometheusModel.Sample {
	return []prometheusModel.Sample{
		homeSample("node_hwmon_temp_celsius", 52.85, "chip", "nvme_nvme0", "sensor", "temp1"),
		homeSample("node_hwmon_temp_celsius", 52.85, "chip", "nvme_nvme0", "sensor", "temp2"),
		homeSample("node_hwmon_temp_celsius", 59.85, "chip", "nvme_nvme0", "sensor", "temp3"),
		homeSample("node_hwmon_temp_celsius", cpu-2, "chip", "platform_coretemp_0", "sensor", "temp1"),
		homeSample("node_hwmon_temp_celsius", cpu, "chip", "platform_coretemp_0", "sensor", "temp2"),
		homeSample("node_hwmon_temp_celsius", cpu, "chip", "platform_coretemp_0", "sensor", "temp3"),
		homeSample("node_hwmon_temp_celsius", 27.8, "chip", "thermal_thermal_zone0", "sensor", "temp0"),
		homeSample("node_hwmon_temp_crit_celsius", 92.85000000000001, "chip", "nvme_nvme0", "sensor", "temp1"),
		homeSample("node_hwmon_temp_crit_celsius", 105, "chip", "platform_coretemp_0", "sensor", "temp1"),
		homeSample("node_hwmon_temp_max_celsius", 85.85000000000001, "chip", "nvme_nvme0", "sensor", "temp1"),
		// мусор прошивки NVMe — не предел
		homeSample("node_hwmon_temp_max_celsius", 65261.85, "chip", "nvme_nvme0", "sensor", "temp2"),
		homeSample("node_hwmon_temp_max_celsius", 105, "chip", "platform_coretemp_0", "sensor", "temp1"),
		homeSample("node_hwmon_sensor_label", 1, "chip", "nvme_nvme0", "sensor", "temp1", "label", "Composite"),
		homeSample("node_hwmon_sensor_label", 1, "chip", "nvme_nvme0", "sensor", "temp3", "label", "Sensor 2"),
		homeSample("node_hwmon_sensor_label", 1, "chip", "platform_coretemp_0", "sensor", "temp1", "label", "Package id 0"),
		homeSample("node_hwmon_sensor_label", 1, "chip", "platform_coretemp_0", "sensor", "temp2", "label", "Core 0"),
		homeSample("node_hwmon_sensor_label", 1, "chip", "platform_coretemp_0", "sensor", "temp3", "label", "Core 1"),
		homeSample("node_hwmon_chip_names", 1, "chip", "nvme_nvme0", "chip_name", "nvme"),
		homeSample("node_hwmon_chip_names", 1, "chip", "platform_coretemp_0", "chip_name", "coretemp"),
		homeSample("node_hwmon_chip_names", 1, "chip", "thermal_thermal_zone0", "chip_name", "acpitz"),
		// thermal zone при hwmon не нужна
		homeSample("node_thermal_zone_temp", 45, "type", "x86_pkg_temp", "zone", "1"),
	}
}

func homeUname() []prometheusModel.Sample {
	return []prometheusModel.Sample{homeSample("node_uname_info", 1, "nodename", "home")}
}

func TestNodeTemperatures_Hwmon(t *testing.T) {
	temps := nodeTemperatures(homeSamples(48), homeUname())
	require.Len(t, temps, 1)
	home := temps[0]
	assert.Equal(t, "home", home.Node, "имя ноды — из node_uname_info по job+instance")
	assert.InDelta(t, 59.85, home.MaxCelsius, 0.001)

	require.Len(t, home.Devices, 3, "по устройству, thermal zone при hwmon не нужна")
	nvme, cpu, board := home.Devices[0], home.Devices[1], home.Devices[2]

	assert.Equal(t, "nvme", nvme.Name)
	assert.Equal(t, "Sensor 2", nvme.Sensor, "самый горячий датчик")
	require.NotNil(t, nvme.LimitCelsius)
	assert.InDelta(t, 85.85, *nvme.LimitCelsius, 0.001, "наименьший из crit/max, мусор 65261 отброшен")

	assert.Equal(t, "coretemp", cpu.Name)
	assert.Equal(t, "Core 0", cpu.Sensor, "равные значения — датчик с меньшим именем")
	assert.InDelta(t, 48, cpu.Celsius, 0.001)

	assert.Equal(t, "acpitz", board.Name)
	assert.Equal(t, "temp0", board.Sensor, "без подписи — имя датчика")
	assert.Nil(t, board.LimitCelsius)
}

func TestNodeTemperatures_ThermalZoneAndNoUname(t *testing.T) {
	samples := []prometheusModel.Sample{
		{Labels: map[string]string{"__name__": "node_thermal_zone_temp", "instance": "10.0.0.5:9100", "type": "cpu-thermal", "zone": "0"}, Value: 61.3},
	}
	temps := nodeTemperatures(samples, nil)
	require.Len(t, temps, 1)
	assert.Equal(t, "10.0.0.5", temps[0].Node, "нет node_uname_info — хост из instance")
	require.Len(t, temps[0].Devices, 1)
	assert.Equal(t, "cpu-thermal", temps[0].Devices[0].Name)
	assert.Equal(t, "thermal_zone0", temps[0].Devices[0].Chip)
}

func TestNodeTemperatures_Empty(t *testing.T) {
	assert.Empty(t, nodeTemperatures(nil, homeUname()))
}

// tempProm — Prometheus домашнего сервера: датчики и node_uname_info, остальное — единица.
type tempProm struct{ cpu float64 }

func (p tempProm) Query(_ context.Context, promql string, _ time.Time) ([]prometheusModel.Sample, error) {
	switch promql {
	case tempQuery:
		return homeSamples(p.cpu), nil
	case unameQuery:
		return homeUname(), nil
	}
	return []prometheusModel.Sample{{Value: 1}}, nil
}

func TestHealth_NodeTemperatures(t *testing.T) {
	k8s := &fakeK8s{nodes: []k8sModel.Node{{Name: "home", Ready: true}}}

	h, err := newUsecase(k8s, fakeAM{}, tempProm{cpu: 48}).Health(context.Background(), time.Hour)
	require.NoError(t, err)
	require.Len(t, h.Nodes.Temperatures, 1)
	assert.False(t, h.Nodes.Temperatures[0].Hot)
	assert.Empty(t, h.Nodes.Problems)
	assert.Equal(t, snapshotModel.HealthHealthy, h.Health)

	// процессор у предела 105 °C (запас 5) — перегрев: проблема ноды и деградация
	h, err = newUsecase(k8s, fakeAM{}, tempProm{cpu: 101}).Health(context.Background(), time.Hour)
	require.NoError(t, err)
	assert.True(t, h.Nodes.Temperatures[0].Hot)
	assert.Equal(t, []clusterModel.NodeProblem{{Name: "home", Problems: []string{clusterModel.NodeProblemOverheating}}}, h.Nodes.Problems)
	assert.Equal(t, snapshotModel.HealthDegraded, h.Health)
	assert.Contains(t, h.SummaryHints, "нода home: Overheating — перегрев: coretemp 101°C при пределе 105°C")

	// виртуальные машины: датчиков нет — поля нет, ошибок нет
	h, err = newUsecase(k8s, fakeAM{}, fakeProm{}).Health(context.Background(), time.Hour)
	require.NoError(t, err)
	assert.Empty(t, h.Nodes.Temperatures)
	assert.Empty(t, h.Errors)
}

// TestLiveNodeTemperatures — запросы к настоящему Prometheus (node-exporter с датчиками):
// PROMETHEUS_LIVE_URL=http://localhost:9090 go test ./internal/usecase/cluster/ -run TestLive -v
func TestLiveNodeTemperatures(t *testing.T) {
	url := os.Getenv("PROMETHEUS_LIVE_URL")
	if url == "" {
		t.Skip("PROMETHEUS_LIVE_URL is not set")
	}
	prom := prometheusService.New(url, prometheusService.Auth{})
	samples, err := prom.Query(context.Background(), tempQuery, time.Now())
	require.NoError(t, err)
	uname, err := prom.Query(context.Background(), unameQuery, time.Now())
	require.NoError(t, err)

	temps := nodeTemperatures(samples, uname)
	require.NotEmpty(t, temps)
	for _, n := range temps {
		for _, d := range n.Devices {
			t.Logf("%s: %s (%s) %.1f°C, limit %v", n.Node, d.Name, d.Sensor, d.Celsius, lo.FromPtr(d.LimitCelsius))
		}
	}
}
