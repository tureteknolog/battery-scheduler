package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SMHIService hämtar väderprognos från SMHI
type SMHIService struct {
	lat float64
	lon float64
}

// SMHIResponse representerar svaret från SMHI API
type SMHIResponse struct {
	TimeSeries []SMHITimeSeries `json:"timeSeries"`
}

type SMHITimeSeries struct {
	Time string       `json:"time"`
	Data SMHIDataItem `json:"data"`
}

type SMHIDataItem struct {
	AirTemperature float64 `json:"air_temperature"`
}

// TemperatureForecast är en temperaturprognos för en tidpunkt
type TemperatureForecast struct {
	Time        time.Time `json:"time"`
	Temperature float64   `json:"temperature"`
}

// NewSMHIService skapar en ny SMHI-tjänst
func NewSMHIService(lat, lon float64) *SMHIService {
	return &SMHIService{lat: lat, lon: lon}
}

// FetchForecast hämtar temperaturprognos från SMHI
func (s *SMHIService) FetchForecast() ([]TemperatureForecast, error) {
	url := fmt.Sprintf(
		"https://opendata-download-metfcst.smhi.se/api/category/snow1g/version/1/geotype/point/lon/%.5f/lat/%.5f/data.json",
		s.lon, s.lat,
	)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch SMHI forecast: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SMHI API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read SMHI response: %w", err)
	}

	var smhiResp SMHIResponse
	if err := json.Unmarshal(body, &smhiResp); err != nil {
		return nil, fmt.Errorf("failed to parse SMHI response: %w", err)
	}

	var forecasts []TemperatureForecast
	for _, ts := range smhiResp.TimeSeries {
		t, err := time.Parse(time.RFC3339, ts.Time)
		if err != nil {
			continue
		}
		forecasts = append(forecasts, TemperatureForecast{
			Time:        t,
			Temperature: ts.Data.AirTemperature,
		})
	}

	return forecasts, nil
}

// GetTemperatureAt returnerar interpolerad temperatur vid en given tidpunkt
func (s *SMHIService) GetTemperatureAt(forecasts []TemperatureForecast, t time.Time) float64 {
	if len(forecasts) == 0 {
		return 5.0 // Fallback
	}

	// Hitta närmaste prognosvärden före och efter t
	var before, after *TemperatureForecast
	for i := range forecasts {
		if !forecasts[i].Time.After(t) {
			before = &forecasts[i]
		}
		if forecasts[i].Time.After(t) {
			after = &forecasts[i]
			break
		}
	}

	if before == nil && after == nil {
		return 5.0
	}
	if before == nil {
		return after.Temperature
	}
	if after == nil {
		return before.Temperature
	}

	// Linjär interpolering
	totalDuration := after.Time.Sub(before.Time).Seconds()
	if totalDuration == 0 {
		return before.Temperature
	}
	elapsed := t.Sub(before.Time).Seconds()
	ratio := elapsed / totalDuration

	return before.Temperature + ratio*(after.Temperature-before.Temperature)
}

// ConsumptionFromTemperature beräknar prognosticerad förbrukning (kW) baserat på temperatur
// Värmepump: y = 0.0041x² - 0.1767x + 2.4392
// Plus tomgångslast för huset: 1.0 kW
func ConsumptionFromTemperature(tempC float64) float64 {
	heatPump := 0.0041*tempC*tempC - 0.1767*tempC + 2.4392
	baseLoad := 1.0
	return heatPump + baseLoad
}
