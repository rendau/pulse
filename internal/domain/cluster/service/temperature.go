package service

import (
	"fmt"
	"strings"

	"github.com/samber/lo"

	"github.com/rendau/pulse/internal/domain/cluster/model"
)

// TempConfig — пороги перегрева нод.
type TempConfig struct {
	// Margin — перегрев, когда до предела датчика (crit/max) осталось не больше стольких градусов
	Margin float64
	// Hot — перегрев устройства без предела в датчике (плата, thermal zone), градусы
	Hot float64
}

func (c *TempConfig) defaults() {
	if c.Margin <= 0 {
		c.Margin = 5
	}
	if c.Hot <= 0 {
		c.Hot = 90
	}
}

// Overheat отмечает перегретые устройства и ноды; перегретая нода получает проблему
// Overheating (как давление памяти или диска — кластер деградировал).
func (s *Service) Overheat(nodes *model.Nodes) {
	for i := range nodes.Temperatures {
		t := &nodes.Temperatures[i]
		t.Hot = false
		for j := range t.Devices {
			d := &t.Devices[j]
			if d.LimitCelsius != nil {
				d.Hot = d.Celsius >= *d.LimitCelsius-s.conf.Temp.Margin
			} else {
				d.Hot = d.Celsius >= s.conf.Temp.Hot
			}
			t.Hot = t.Hot || d.Hot
		}
		if !t.Hot {
			continue
		}
		_, idx, found := lo.FindIndexOf(nodes.Problems, func(p model.NodeProblem) bool { return p.Name == t.Node })
		if !found {
			nodes.Problems = append(nodes.Problems, model.NodeProblem{Name: t.Node})
			idx = len(nodes.Problems) - 1
		}
		if !lo.Contains(nodes.Problems[idx].Problems, model.NodeProblemOverheating) {
			nodes.Problems[idx].Problems = append(nodes.Problems[idx].Problems, model.NodeProblemOverheating)
		}
	}
}

// heatDetails — перегретые устройства ноды словами: «nvme 83°C при пределе 86°C».
func heatDetails(nodes model.Nodes, node string) string {
	t, ok := lo.Find(nodes.Temperatures, func(t model.NodeTemperature) bool { return t.Node == node })
	if !ok {
		return ""
	}
	hot := lo.FilterMap(t.Devices, func(d model.TempDevice, _ int) (string, bool) {
		text := fmt.Sprintf("%s %.0f°C", d.Name, d.Celsius)
		if d.LimitCelsius != nil {
			text += fmt.Sprintf(" при пределе %.0f°C", *d.LimitCelsius)
		}
		return text, d.Hot
	})
	return strings.Join(hot, ", ")
}
