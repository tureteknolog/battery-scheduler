package services

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// priceXML är ett minimalt Entsoe-svar med två kvartspriser
const priceXML = `<?xml version="1.0" encoding="UTF-8"?>
<Publication_MarketDocument>
  <TimeSeries>
    <Period>
      <timeInterval><start>2026-08-30T22:00Z</start><end>2026-08-31T22:00Z</end></timeInterval>
      <Point><position>1</position><price.amount>50.0</price.amount></Point>
      <Point><position>2</position><price.amount>60.0</price.amount></Point>
    </Period>
  </TimeSeries>
</Publication_MarketDocument>`

// maintenanceHTML liknar underhållssidan Entsoe svarar med vid driftstopp
var maintenanceHTML = "<!doctype html><html><head><title>Transparency Platform</title></head>" +
	strings.Repeat("<p>Scheduled maintenance is currently underway.</p>", 50) + "</html>"

// newTestService ger en service som pratar med en lokal testserver i stället
// för Entsoe
func newTestService(t *testing.T, handler http.HandlerFunc) (*EntsoeService, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	svc := NewEntsoeService("test-token", "SE3")
	svc.baseURL = server.URL
	return svc, server.Close
}

func testWindow() (time.Time, time.Time) {
	from := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	return from, from.Add(48 * time.Hour)
}

func TestFetchPricesWithRetrySucceedsAfterMaintenance(t *testing.T) {
	var calls int
	svc, closeServer := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(maintenanceHTML))
			return
		}
		w.Write([]byte(priceXML))
	})
	defer closeServer()

	from, to := testWindow()
	prices, err := svc.FetchPricesWithRetry(from, to, 5, time.Millisecond)
	if err != nil {
		t.Fatalf("förväntade lyckad hämtning, fick: %v", err)
	}
	if calls != 3 {
		t.Errorf("förväntade 3 anrop, fick %d", calls)
	}
	if len(prices) == 0 {
		t.Error("förväntade priser i svaret")
	}
}

func TestFetchPricesWithRetryGivesUpAfterAllAttempts(t *testing.T) {
	var calls int
	svc, closeServer := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(maintenanceHTML))
	})
	defer closeServer()

	from, to := testWindow()
	_, err := svc.FetchPricesWithRetry(from, to, 4, time.Millisecond)
	if err == nil {
		t.Fatal("förväntade fel när alla försök misslyckas")
	}
	if calls != 4 {
		t.Errorf("förväntade 4 anrop, fick %d", calls)
	}
	if !strings.Contains(err.Error(), "gav upp efter 4 försök") {
		t.Errorf("felmeddelandet nämner inte antalet försök: %v", err)
	}

	// Felet får inte dra med sig hela HTML-sidan in i loggen och Pushover-notisen
	if len(err.Error()) > 400 {
		t.Errorf("felmeddelandet är inte nedkortat (%d tecken): %.120s...", len(err.Error()), err.Error())
	}
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("statuskoden bevarades inte i felet: %v", err)
	}
}

func TestFetchPricesWithRetryStopsOnPermanentError(t *testing.T) {
	var calls int
	svc, closeServer := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Unauthorized"))
	})
	defer closeServer()

	from, to := testWindow()
	_, err := svc.FetchPricesWithRetry(from, to, 5, time.Minute)
	if err == nil {
		t.Fatal("förväntade fel vid 401")
	}
	if calls != 1 {
		t.Errorf("401 ska inte göras om, men servern anropades %d gånger", calls)
	}
}

func TestFetchPricesWithRetryStopsWhenTokenMissing(t *testing.T) {
	var calls int
	svc, closeServer := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
	})
	defer closeServer()
	svc.token = ""

	from, to := testWindow()
	_, err := svc.FetchPricesWithRetry(from, to, 5, time.Minute)
	if !errors.Is(err, ErrMissingToken) {
		t.Fatalf("förväntade ErrMissingToken, fick: %v", err)
	}
	if calls != 0 {
		t.Errorf("utan token ska inget anrop göras, men servern anropades %d gånger", calls)
	}
}

func TestFetchPricesTreatsEmptyResponseAsError(t *testing.T) {
	svc, closeServer := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><Publication_MarketDocument></Publication_MarketDocument>`))
	})
	defer closeServer()

	from, to := testWindow()
	_, err := svc.FetchPrices(from, to)
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("förväntade ErrNoData för svar utan priser, fick: %v", err)
	}
	if !retryable(err) {
		t.Error("ännu opublicerad data ska räknas som tillfälligt fel")
	}
}
