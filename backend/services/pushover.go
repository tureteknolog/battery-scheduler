package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type PushoverService struct {
	appToken string
	userKey  string
	apiURL   string
}

// pushoverAPIURL är Pushovers endpoint för att skicka meddelanden
const pushoverAPIURL = "https://api.pushover.net/1/messages.json"

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
		apiURL:   pushoverAPIURL,
	}
}

// pushoverClient har timeout så att en notis inte kan hänga sig fast
var pushoverClient = &http.Client{Timeout: 30 * time.Second}

// SendNotification skickar en push-notis via Pushover med normal prioritet
func (p *PushoverService) SendNotification(message, title, url string) error {
	return p.SendNotificationWithPriority(message, title, url, 0)
}

// SendNotificationWithPriority skickar en push-notis med angiven Pushover-prioritet
func (p *PushoverService) SendNotificationWithPriority(message, title, url string, priority int) error {
	if p.appToken == "" || p.userKey == "" {
		return fmt.Errorf("Pushover credentials saknas - konfigurera i settings")
	}

	payload := PushoverMessage{
		Token:    p.appToken,
		User:     p.userKey,
		Message:  message,
		Title:    title,
		URL:      url,
		Priority: priority,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	resp, err := pushoverClient.Post(
		p.apiURL,
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

// SendPriceUpdateNotification skickar notis om nya elpriser. source säger vilken
// priskälla som svarade - står det något annat än Home Assistant är det en
// reservkälla som bär, vilket är värt att veta innan man litar på priserna in i öret.
func (p *PushoverService) SendPriceUpdateNotification(avgPrice int, minPrice int, maxPrice int, source string, appURL string) error {
	message := fmt.Sprintf(
		"Morgondagens elpriser är här!\n\nMedelpris: %d öre/kWh\nLägsta: %d öre/kWh\nHögsta: %d öre/kWh\n\nKälla: %s",
		avgPrice, minPrice, maxPrice, source,
	)

	return p.SendNotification(message, "Batteristyrning", appURL)
}

// SendPriceFetchFailedNotification varnar när prishämtningen inte gick igenom.
// Prioritet 1 (hög): utan nya priser går schemat på gamla data, så notisen ska
// ta sig förbi tysta timmar.
func (p *PushoverService) SendPriceFetchFailedNotification(summary string, cause error, appURL string) error {
	message := fmt.Sprintf(
		"%s\n\nOrsak: %s\n\nBatteriet fortsätter på det schema som redan är sparat. Kör en manuell uppdatering när tjänsten är tillbaka.",
		summary, truncate(cause.Error(), 400),
	)

	return p.SendNotificationWithPriority(message, "Batteristyrning: priserna uppdaterades inte", appURL, 1)
}
