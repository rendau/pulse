package model

import (
	"time"

	commonModel "github.com/rendau/pulse/internal/domain/common/model"
)

// Main — факт деплоя: у workload сменился образ или digest запущенного образа.
type Main struct {
	Id              int64
	Cluster         string
	Namespace       string
	Kind            string
	Name            string
	ServiceName     string
	Image           string
	ImageDigest     string
	DeployedCommit  string
	PrevImage       string
	PrevImageDigest string
	PrevCommit      string
	ObservedAt      time.Time
}

// Edit — мутация (все поля pointer-типы)
type Edit struct {
	Cluster         *string
	Namespace       *string
	Kind            *string
	Name            *string
	ServiceName     *string
	Image           *string
	ImageDigest     *string
	DeployedCommit  *string
	PrevImage       *string
	PrevImageDigest *string
	PrevCommit      *string
	ObservedAt      *time.Time
}

// ListReq — параметры выборки
type ListReq struct {
	commonModel.ListParams

	Cluster      *string
	ServiceNames []string
	Since        *time.Time
}
