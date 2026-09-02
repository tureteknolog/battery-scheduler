package services

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"battery-scheduler/models"
)

// stubSource är en priskälla med förutbestämt svar
type stubSource struct {
	name   string
	prices []models.Price
	err    error
	calls  int
}

func (s *stubSource) Name() string { return s.name }

func (s *stubSource) FetchPrices(from, to time.Time) ([]models.Price, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.prices, nil
}

func onePrice() []models.Price {
	return []models.Price{{Timestamp: time.Now(), PriceOre: 100, Area: "SE3"}}
}

func TestFallsBackToSecondSourceInTheSameAttempt(t *testing.T) {
	down := &stubSource{name: "Entsoe", err: &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Body: "maintenance"}}
	backup := &stubSource{name: "elprisetjustnu.se", prices: onePrice()}

	from, to := testWindow()
	// En timmes backoff: går testet igenom snabbt har reserven provats direkt,
	// utan att vänta ut Entsoes nästa försök
	prices, source, err := FetchPricesWithRetry([]PriceSource{down, backup}, from, to, 5, time.Hour)
	if err != nil {
		t.Fatalf("förväntade priser från reservkällan, fick: %v", err)
	}
	if source != "elprisetjustnu.se" {
		t.Errorf("fel källa rapporterades: %q", source)
	}
	if len(prices) != 1 {
		t.Errorf("förväntade 1 pris, fick %d", len(prices))
	}
	if down.calls != 1 || backup.calls != 1 {
		t.Errorf("förväntade ett anrop per källa, fick %d respektive %d", down.calls, backup.calls)
	}
}

func TestPrefersTheFirstSourceWhenItWorks(t *testing.T) {
	primary := &stubSource{name: "Entsoe", prices: onePrice()}
	backup := &stubSource{name: "elprisetjustnu.se", prices: onePrice()}

	from, to := testWindow()
	_, source, err := FetchPricesWithRetry([]PriceSource{primary, backup}, from, to, 5, time.Millisecond)
	if err != nil {
		t.Fatalf("förväntade lyckad hämtning, fick: %v", err)
	}
	if source != "Entsoe" {
		t.Errorf("Entsoe ska användas när den svarar, fick %q", source)
	}
	if backup.calls != 0 {
		t.Errorf("reservkällan ska inte anropas i onödan, anropades %d gånger", backup.calls)
	}
}

func TestRetriesAllSourcesUntilAttemptsRunOut(t *testing.T) {
	down := &stubSource{name: "Entsoe", err: &HTTPStatusError{StatusCode: http.StatusServiceUnavailable}}
	alsoDown := &stubSource{name: "elprisetjustnu.se", err: errors.New("nätverksfel")}

	from, to := testWindow()
	_, _, err := FetchPricesWithRetry([]PriceSource{down, alsoDown}, from, to, 3, time.Millisecond)
	if err == nil {
		t.Fatal("förväntade fel när ingen källa svarar")
	}
	if down.calls != 3 || alsoDown.calls != 3 {
		t.Errorf("förväntade 3 försök per källa, fick %d respektive %d", down.calls, alsoDown.calls)
	}
	if !strings.Contains(err.Error(), "gav upp efter 3 försök") {
		t.Errorf("felmeddelandet nämner inte antalet försök: %v", err)
	}
	if !strings.Contains(err.Error(), "elprisetjustnu.se") {
		t.Errorf("felmeddelandet säger inte vilken källa som sist misslyckades: %v", err)
	}
}

func TestStopsImmediatelyWhenEverySourceFailsPermanently(t *testing.T) {
	noToken := &stubSource{name: "Entsoe", err: ErrMissingToken}
	notPublished := &stubSource{name: "elprisetjustnu.se", err: &HTTPStatusError{StatusCode: http.StatusNotFound}}

	from, to := testWindow()
	// En timmes backoff igen: väntar koden är felet att den tror sig kunna försöka igen
	_, _, err := FetchPricesWithRetry([]PriceSource{noToken, notPublished}, from, to, 5, time.Hour)
	if err == nil {
		t.Fatal("förväntade fel")
	}
	if noToken.calls != 1 || notPublished.calls != 1 {
		t.Errorf("permanenta fel ska inte göras om, fick %d respektive %d anrop", noToken.calls, notPublished.calls)
	}
}

func TestFallbackStillRunsWhenEntsoeTokenIsMissing(t *testing.T) {
	noToken := &stubSource{name: "Entsoe", err: ErrMissingToken}
	backup := &stubSource{name: "elprisetjustnu.se", prices: onePrice()}

	from, to := testWindow()
	_, source, err := FetchPricesWithRetry([]PriceSource{noToken, backup}, from, to, 5, time.Millisecond)
	if err != nil {
		t.Fatalf("utan token ska reservkällan bära, fick: %v", err)
	}
	if source != "elprisetjustnu.se" {
		t.Errorf("fel källa rapporterades: %q", source)
	}
}
