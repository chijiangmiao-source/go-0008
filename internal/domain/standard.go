package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

type MeasurementKind string

const (
	MeasureConcentration MeasurementKind = "concentration"
	MeasureTemperature   MeasurementKind = "temperature"
	MeasurePressure      MeasurementKind = "pressure"
)

type SafetyStandardVersion struct {
	Version             string              `json:"version"`
	Source              string              `json:"source"`
	EffectiveAt         time.Time           `json:"effective_at"`
	MinConcentrationPPM float64             `json:"min_concentration_ppm"`
	MaxResidualPPM      float64             `json:"max_residual_ppm"`
	MinTemperatureC     float64             `json:"min_temperature_c"`
	PressureMinKPA      float64             `json:"pressure_min_kpa"`
	PressureMaxKPA      float64             `json:"pressure_max_kpa"`
	RequiredDuration    time.Duration       `json:"required_duration"`
	MaxSampleGap        time.Duration       `json:"max_sample_gap"`
	LateWindow          time.Duration       `json:"late_window"`
	UnitConversions     map[string]UnitRule `json:"unit_conversions"`
	Digest              string              `json:"digest"`
}

type UnitRule struct {
	Kind   MeasurementKind `json:"kind"`
	Scale  float64         `json:"scale"`
	Offset float64         `json:"offset"`
	Target string          `json:"target"`
}

func DefaultStandards(anchor time.Time) []SafetyStandardVersion {
	v1 := SafetyStandardVersion{
		Version:             "CN-GRAIN-SAMPLE-2026-A",
		Source:              "desensitized-public-grain-fumigation-sample",
		EffectiveAt:         anchor.Add(-24 * time.Hour),
		MinConcentrationPPM: 280,
		MaxResidualPPM:      2.0,
		MinTemperatureC:     8,
		PressureMinKPA:      94,
		PressureMaxKPA:      108,
		RequiredDuration:    90 * time.Minute,
		MaxSampleGap:        20 * time.Minute,
		LateWindow:          10 * time.Minute,
		UnitConversions:     defaultUnitRules(),
	}
	v2 := v1
	v2.Version = "CN-GRAIN-SAMPLE-2026-B"
	v2.EffectiveAt = anchor.Add(45 * time.Minute)
	v2.MinConcentrationPPM = 300
	v2.MaxResidualPPM = 1.6
	v1.Digest = v1.ContentDigest()
	v2.Digest = v2.ContentDigest()
	return []SafetyStandardVersion{v1, v2}
}

func defaultUnitRules() map[string]UnitRule {
	return map[string]UnitRule{
		"ppm":  {Kind: MeasureConcentration, Scale: 1, Offset: 0, Target: "ppm"},
		"g/m3": {Kind: MeasureConcentration, Scale: 244, Offset: 0, Target: "ppm"},
		"c":    {Kind: MeasureTemperature, Scale: 1, Offset: 0, Target: "c"},
		"f":    {Kind: MeasureTemperature, Scale: 5.0 / 9.0, Offset: -32 * 5.0 / 9.0, Target: "c"},
		"kpa":  {Kind: MeasurePressure, Scale: 1, Offset: 0, Target: "kpa"},
		"pa":   {Kind: MeasurePressure, Scale: 0.001, Offset: 0, Target: "kpa"},
	}
}

func (s SafetyStandardVersion) Convert(unit string, value float64) (MeasurementKind, float64, string, error) {
	rule, ok := s.UnitConversions[unit]
	if !ok {
		return "", 0, "", BoundaryError{Boundary: BoundaryInput, Code: "unknown_unit", Message: unit}
	}
	return rule.Kind, value*rule.Scale + rule.Offset, rule.Target, nil
}

func (s SafetyStandardVersion) ContentDigest() string {
	keys := make([]string, 0, len(s.UnitConversions))
	for k := range s.UnitConversions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	text := fmt.Sprintf("%s|%s|%s|%.3f|%.3f|%.3f|%.3f|%.3f|%s|%s|",
		s.Version, s.Source, s.EffectiveAt.UTC().Format(time.RFC3339Nano), s.MinConcentrationPPM,
		s.MaxResidualPPM, s.MinTemperatureC, s.PressureMinKPA, s.PressureMaxKPA, s.RequiredDuration, s.MaxSampleGap)
	for _, k := range keys {
		r := s.UnitConversions[k]
		text += fmt.Sprintf("%s:%s:%.6f:%.6f:%s|", k, r.Kind, r.Scale, r.Offset, r.Target)
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

type Standards struct {
	Versions []SafetyStandardVersion
}

func (s Standards) ForRegistration(at time.Time) (SafetyStandardVersion, error) {
	return s.ForEventTime(at)
}

func (s Standards) ForEventTime(at time.Time) (SafetyStandardVersion, error) {
	var found *SafetyStandardVersion
	for i := range s.Versions {
		v := s.Versions[i]
		if !v.EffectiveAt.After(at) {
			cp := v
			found = &cp
		}
	}
	if found == nil {
		return SafetyStandardVersion{}, BoundaryError{Boundary: BoundaryStandard, Code: "standard_missing", Message: "no standard effective at event time"}
	}
	if found.Digest != "" && found.Digest != found.ContentDigest() {
		return SafetyStandardVersion{}, BoundaryError{Boundary: BoundaryStandard, Code: "standard_digest_mismatch", Message: found.Version}
	}
	return *found, nil
}

func (s Standards) ByVersion(version string) (SafetyStandardVersion, bool) {
	for _, v := range s.Versions {
		if v.Version == version {
			return v, true
		}
	}
	return SafetyStandardVersion{}, false
}
