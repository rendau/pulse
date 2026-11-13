package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/rendau/pulse/internal/domain/cluster/model"
)

func TestOverheat(t *testing.T) {
	nodes := model.Nodes{
		Problems: []model.NodeProblem{{Name: "n1", Problems: []string{"MemoryPressure"}}},
		Temperatures: []model.NodeTemperature{
			{Node: "n1", Devices: []model.TempDevice{
				{Name: "nvme", Celsius: 82, LimitCelsius: new(85.85)},
				{Name: "coretemp", Celsius: 70, LimitCelsius: new(105.0)},
			}},
			// без предела в датчике — порог hot
			{Node: "n2", Devices: []model.TempDevice{{Name: "acpitz", Celsius: 91}}},
			{Node: "n3", Devices: []model.TempDevice{{Name: "acpitz", Celsius: 60}}},
		},
	}

	s := New(Config{})
	s.Overheat(&nodes)
	s.Overheat(&nodes) // повтор не дублирует проблему

	assert.True(t, nodes.Temperatures[0].Hot)
	assert.True(t, nodes.Temperatures[0].Devices[0].Hot)
	assert.False(t, nodes.Temperatures[0].Devices[1].Hot)
	assert.True(t, nodes.Temperatures[1].Hot)
	assert.False(t, nodes.Temperatures[2].Hot)
	assert.Equal(t, []model.NodeProblem{
		{Name: "n1", Problems: []string{"MemoryPressure", model.NodeProblemOverheating}},
		{Name: "n2", Problems: []string{model.NodeProblemOverheating}},
	}, nodes.Problems)

	hints := s.SummaryHints(&model.Health{Nodes: nodes}, time.Now())
	assert.Contains(t, hints, "нода n1: MemoryPressure, Overheating — перегрев: nvme 82°C при пределе 86°C")
	assert.Contains(t, hints, "нода n2: Overheating — перегрев: acpitz 91°C")
}
