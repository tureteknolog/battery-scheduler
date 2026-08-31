package services

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendPriceFetchFailedNotification(t *testing.T) {
	var got PushoverMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("kunde inte läsa payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := NewPushoverService("app-token", "user-key")
	svc.apiURL = server.URL

	// Ett realistiskt fel: hela underhållssidan som orsak
	cause := &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Body: maintenanceHTML}
	err := svc.SendPriceFetchFailedNotification("Kunde inte hämta elpriser från Entsoe efter 5 försök.", cause, "http://battery.local")
	if err != nil {
		t.Fatalf("förväntade lyckad notis, fick: %v", err)
	}

	if got.Priority != 1 {
		t.Errorf("förväntade prioritet 1 (hög), fick %d", got.Priority)
	}
	if !strings.Contains(got.Message, "efter 5 försök") {
		t.Errorf("meddelandet saknar sammanfattningen: %q", got.Message)
	}
	if !strings.Contains(got.Message, "status 503") {
		t.Errorf("meddelandet saknar orsaken: %q", got.Message)
	}
	if got.URL != "http://battery.local" {
		t.Errorf("förväntade länk till appen, fick %q", got.URL)
	}
	// Pushover kapar meddelanden över 1024 tecken - HTML-sidan får inte fylla ut det
	if len([]rune(got.Message)) > 1024 {
		t.Errorf("meddelandet är för långt (%d tecken)", len([]rune(got.Message)))
	}
}

func TestSendPriceFetchFailedNotificationWithoutCredentials(t *testing.T) {
	svc := NewPushoverService("", "")
	err := svc.SendPriceFetchFailedNotification("Kunde inte hämta priser.", errors.New("nätverksfel"), "")
	if err == nil {
		t.Fatal("förväntade fel när Pushover-uppgifter saknas")
	}
}
