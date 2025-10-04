package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type PushoverService struct {
	appToken string
	userKey  string
}

type PushoverMessage struct {
	Token    string `json:"token"`
	User     string `json:"user"`
	Message  string `json:"message"`
	Title    string `json:"title,omitempty"`
	URL      string `json:"url,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

// NewPushoverService skapar en ny Pushover-service
func NewPushoverService(appToken, userKey string) *PushoverService {
	return &PushoverService{
		appToken: appToken,
		userKey:  userKey,
	}
}

// SendNotification skickar en push-notis via Pushover
func (p *PushoverService) SendNotification(message, title, url string) error {
	if p.appToken == "" || p.userKey == "" {
		return fmt.Errorf("Pushover credentials saknas - konfigurera i settings")
	}

	payload := PushoverMessage{
		Token:    p.appToken,
		User:     p.userKey,
		Message:  message,
		Title:    title,
		URL:      url,
		Priority: 0, // Normal priority
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	resp, err := http.Post(
		"https://api.pushover.net/1/messages.json",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return fmt.Errorf("failed to send notification: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Pushover API returned status %d", resp.StatusCode)
	}

	return nil
}

// SendPriceUpdateNotification skickar notis om nya elpriser
func (p *PushoverService) SendPriceUpdateNotification(avgPrice int, minPrice int, maxPrice int, appURL string) error {
	message := fmt.Sprintf(
		"Morgondagens elpriser är här!\n\nMedelpris: %d öre/kWh\nLägsta: %d öre/kWh\nHögsta: %d öre/kWh",
		avgPrice, minPrice, maxPrice,
	)

	return p.SendNotification(message, "Batteristyrning", appURL)
}
