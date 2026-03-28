package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// HomeAssistantService hämtar data från Home Assistant
type HomeAssistantService struct {
	baseURL string
	token   string
}

// HAStateResponse representerar ett state-svar från Home Assistant
type HAStateResponse struct {
	EntityID    string    `json:"entity_id"`
	State       string    `json:"state"`
	LastChanged time.Time `json:"last_changed"`
}

// NewHomeAssistantService skapar en ny Home Assistant-tjänst
func NewHomeAssistantService(baseURL, token string) *HomeAssistantService {
	return &HomeAssistantService{
		baseURL: baseURL,
		token:   token,
	}
}

// GetSoC hämtar aktuell State of Charge från Ferroamp-sensorn
func (h *HomeAssistantService) GetSoC() (float64, time.Time, error) {
	if h.baseURL == "" || h.token == "" {
		return 0, time.Time{}, fmt.Errorf("Home Assistant not configured")
	}

	url := fmt.Sprintf("%s/api/states/sensor.ferroamp_system_state_of_charge", h.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to fetch SoC from Home Assistant: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, time.Time{}, fmt.Errorf("Home Assistant returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to read response: %w", err)
	}

	var state HAStateResponse
	if err := json.Unmarshal(body, &state); err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to parse response: %w", err)
	}

	soc, err := strconv.ParseFloat(state.State, 64)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to parse SoC value '%s': %w", state.State, err)
	}

	return soc, state.LastChanged, nil
}
