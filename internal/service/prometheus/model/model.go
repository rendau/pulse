package model

import "time"

// Sample — точка instant-запроса.
type Sample struct {
	Labels map[string]string
	TS     time.Time
	Value  float64
}

// Series — ряд range-запроса.
type Series struct {
	Labels map[string]string
	Points []Point
}

type Point struct {
	TS    time.Time
	Value float64
}
