package cluster

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"slices"

	"github.com/samber/lo"

	"github.com/rendau/pulse/internal/constant"
	clusterModel "github.com/rendau/pulse/internal/domain/cluster/model"
	prometheusModel "github.com/rendau/pulse/internal/service/prometheus/model"
)

// Стандартные метрики node-exporter (коллекторы hwmon и thermal_zone включены по умолчанию).
// Имени ноды в них нет — оно в node_uname_info (nodename) того же экспортёра (job + instance).
const (
	tempQuery  = `{__name__=~"node_hwmon_temp_celsius|node_hwmon_temp_crit_celsius|node_hwmon_temp_max_celsius|node_hwmon_sensor_label|node_hwmon_chip_names|node_thermal_zone_temp"}`
	unameQuery = `node_uname_info`

	// tempSaneMax — предел датчика выше этого — мусор прошивки (у NVMe бывает 65261 °C)
	tempSaneMax = 150.0
)

// temperatures — температура нод по датчикам. Датчиков нет (виртуальные машины облака) —
// пусто, без ошибки; Prometheus не подключён — ошибку уже добавил metrics.
func (c *collector) temperatures(ctx context.Context) {
	if c.u.prometheus == nil {
		return
	}

	samples, err := c.u.prometheus.Query(ctx, tempQuery, c.now)
	if err != nil {
		c.addError(constant.SourcePrometheus, fmt.Errorf("node temperatures: %w", err))
		return
	}
	if len(samples) == 0 {
		return
	}
	uname, err := c.u.prometheus.Query(ctx, unameQuery, c.now)
	if err != nil {
		c.addError(constant.SourcePrometheus, fmt.Errorf("node_uname_info: %w", err))
		return
	}

	temps := nodeTemperatures(samples, uname)

	c.mu.Lock()
	c.temps = temps
	c.mu.Unlock()
}

// nodeTemperatures — сэмплы датчиков → температура по нодам: по устройству самый горячий
// датчик и предел (наименьший из crit/max датчиков устройства). Нода без hwmon — по thermal zone.
func nodeTemperatures(samples, uname []prometheusModel.Sample) []clusterModel.NodeTemperature {
	type exporterKey struct{ job, instance string }
	names := make(map[exporterKey]string, len(uname))
	for _, s := range uname {
		if n := s.Labels["nodename"]; n != "" {
			names[exporterKey{s.Labels["job"], s.Labels["instance"]}] = n
		}
	}
	nodeOf := func(labels map[string]string) string {
		if n := names[exporterKey{labels["job"], labels["instance"]}]; n != "" {
			return n
		}
		if n := lo.CoalesceOrEmpty(labels["node"], labels["nodename"]); n != "" {
			return n
		}
		if host, _, err := net.SplitHostPort(labels["instance"]); err == nil {
			return host
		}
		return labels["instance"]
	}

	type sensorKey struct{ node, chip, sensor string }
	type chipKey struct{ node, chip string }
	var (
		values    = map[sensorKey]float64{}
		limits    = map[chipKey]float64{}
		labels    = map[sensorKey]string{}
		chipNames = map[chipKey]string{}
		zones     = map[string][]clusterModel.TempDevice{}
	)
	for _, s := range samples {
		node := nodeOf(s.Labels)
		if node == "" {
			continue
		}
		sk := sensorKey{node, s.Labels["chip"], s.Labels["sensor"]}
		ck := chipKey{node, sk.chip}
		switch s.Labels["__name__"] {
		case "node_hwmon_temp_celsius":
			// повтор одной серии (несколько Prometheus, два скрейпа) — самое высокое значение
			if v, ok := values[sk]; !ok || s.Value > v {
				values[sk] = s.Value
			}
		case "node_hwmon_temp_crit_celsius", "node_hwmon_temp_max_celsius":
			if s.Value > 0 && s.Value <= tempSaneMax {
				if v, ok := limits[ck]; !ok || s.Value < v {
					limits[ck] = s.Value
				}
			}
		case "node_hwmon_sensor_label":
			labels[sk] = s.Labels["label"]
		case "node_hwmon_chip_names":
			chipNames[ck] = s.Labels["chip_name"]
		case "node_thermal_zone_temp":
			zones[node] = append(zones[node], clusterModel.TempDevice{
				Chip: "thermal_zone" + s.Labels["zone"], Name: lo.CoalesceOrEmpty(s.Labels["type"], "thermal_zone"+s.Labels["zone"]), Celsius: s.Value,
			})
		}
	}

	devices := map[string]map[string]*clusterModel.TempDevice{}
	for sk, v := range values {
		if devices[sk.node] == nil {
			devices[sk.node] = map[string]*clusterModel.TempDevice{}
		}
		sensor := lo.CoalesceOrEmpty(labels[sk], sk.sensor)
		d := devices[sk.node][sk.chip]
		if d == nil {
			ck := chipKey{sk.node, sk.chip}
			d = &clusterModel.TempDevice{Chip: sk.chip, Name: lo.CoalesceOrEmpty(chipNames[ck], sk.chip), Sensor: sensor, Celsius: v}
			if limit, ok := limits[ck]; ok {
				d.LimitCelsius = new(limit)
			}
			devices[sk.node][sk.chip] = d
			continue
		}
		// равные значения — датчик с меньшим именем, чтобы ответ не прыгал между вызовами
		if v > d.Celsius || (v == d.Celsius && sensor < d.Sensor) {
			d.Celsius = v
			d.Sensor = sensor
		}
	}

	result := make([]clusterModel.NodeTemperature, 0, max(len(devices), len(zones)))
	for _, node := range lo.Uniq(append(lo.Keys(devices), lo.Keys(zones)...)) {
		list := lo.MapToSlice(devices[node], func(_ string, d *clusterModel.TempDevice) clusterModel.TempDevice { return *d })
		if len(list) == 0 {
			list = zones[node]
		}
		slices.SortFunc(list, func(a, b clusterModel.TempDevice) int {
			return cmp.Or(cmp.Compare(b.Celsius, a.Celsius), cmp.Compare(a.Chip, b.Chip))
		})
		result = append(result, clusterModel.NodeTemperature{Node: node, Devices: list, MaxCelsius: list[0].Celsius})
	}
	slices.SortFunc(result, func(a, b clusterModel.NodeTemperature) int { return cmp.Compare(a.Node, b.Node) })
	return result
}
