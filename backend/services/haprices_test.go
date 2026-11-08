package services

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// haQuarters bygger raw_today/raw_tomorrow med en kvart per pris. nil = null.
func haQuarters(day time.Time, sek ...*float64) string {
	var items []string
	for i, price := range sek {
		start := day.Add(time.Duration(i) * 15 * time.Minute)
		value := "null"
		if price != nil {
			value = fmt.Sprintf("%g", *price)
		}
		items = append(items, fmt.Sprintf(`{"start":"%s","end":"%s","value":%s}`,
			start.Format(time.RFC3339), start.Add(15*time.Minute).Format(time.RFC3339), value))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func kr(v float64) *float64 { return &v }

func haStateJSON(region string, today, tomorrow string, tomorrowValid bool) string {
	return fmt.Sprintf(`{"entity_id":"%s","state":"2.57","attributes":{"region":"%s","currency":"SEK",
		"raw_today":%s,"raw_tomorrow":%s,"tomorrow_valid":%t}}`, DefaultHAPriceEntity, region, today, tomorrow, tomorrowValid)
}

func newTestHAPrices(t *testing.T, handler http.HandlerFunc) (*HAPriceService, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	return NewHAPriceService(server.URL, "testtoken", "", "SE3"), server.Close
}

func TestHAPricesReadsBothDaysAndConvertsToOre(t *testing.T) {
	from, to := elprisetWindow()
	var gotPath, gotAuth string
	svc, done := newTestHAPrices(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Write([]byte(haStateJSON("SE3",
			haQuarters(from, kr(2.01), kr(2.1)),
			haQuarters(from.Add(24*time.Hour), kr(2.36), kr(0.07)), true)))
	})
	defer done()

	prices, err := svc.FetchPrices(from, to)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/states/"+DefaultHAPriceEntity || gotAuth != "Bearer testtoken" {
		t.Errorf("fel anrop: %s med %q", gotPath, gotAuth)
	}
	want := []int{201, 210, 236, 7}
	if len(prices) != len(want) {
		t.Fatalf("fick %d priser, ville ha %d", len(prices), len(want))
	}
	for i, p := range prices {
		// 2.01*100 är 200.99999... i flyttal; avrundningen måste ge 201
		if p.PriceOre != want[i] || p.Area != "SE3" {
			t.Errorf("pris %d: %d öre %s, ville ha %d öre SE3", i, p.PriceOre, p.Area, want[i])
		}
	}
	if !prices[2].Timestamp.Equal(from.Add(24 * time.Hour)) {
		t.Errorf("morgondagens första kvart har tiden %s", prices[2].Timestamp)
	}
}

func TestHAPricesIgnoresTomorrowUntilValid(t *testing.T) {
	from, to := elprisetWindow()
	svc, done := newTestHAPrices(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(haStateJSON("SE3",
			haQuarters(from, kr(1.5)),
			haQuarters(from.Add(24*time.Hour), kr(9.99)), false)))
	})
	defer done()

	prices, err := svc.FetchPrices(from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 1 || prices[0].PriceOre != 150 {
		t.Errorf("bara dagens kvart skulle med, fick %+v", prices)
	}
}

func TestHAPricesSkipsMissingQuartersAndClipsWindow(t *testing.T) {
	from, _ := elprisetWindow()
	to := from.Add(30 * time.Minute)
	svc, done := newTestHAPrices(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(haStateJSON("SE3", haQuarters(from, nil, kr(1.0), kr(2.0)), "[]", false)))
	})
	defer done()

	prices, err := svc.FetchPrices(from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 1 || prices[0].PriceOre != 100 {
		t.Errorf("ville ha bara kvart två (null hoppas över, kvart tre utanför fönstret), fick %+v", prices)
	}
}

func TestHAPricesRefusesWrongArea(t *testing.T) {
	from, to := elprisetWindow()
	svc, done := newTestHAPrices(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(haStateJSON("SE4", haQuarters(from, kr(1.0)), "[]", false)))
	})
	defer done()

	_, err := svc.FetchPrices(from, to)
	if !errors.Is(err, ErrMisconfigured) || retryable(err) {
		t.Errorf("fel elområde ska vara ett permanent fel, fick %v", err)
	}
}

func TestHAPricesEmptyIsRetryableNoData(t *testing.T) {
	from, to := elprisetWindow()
	svc, done := newTestHAPrices(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(haStateJSON("SE3", "[]", "[]", false)))
	})
	defer done()

	_, err := svc.FetchPrices(from, to)
	if !errors.Is(err, ErrNoData) || !retryable(err) {
		t.Errorf("tom sensor ska ge ErrNoData och nytt försök, fick %v", err)
	}
}

func TestHAPricesHTTPErrors(t *testing.T) {
	from, to := elprisetWindow()
	for status, wantRetry := range map[int]bool{http.StatusUnauthorized: false, http.StatusBadGateway: true} {
		svc, done := newTestHAPrices(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		})
		_, err := svc.FetchPrices(from, to)
		done()
		if err == nil || retryable(err) != wantRetry {
			t.Errorf("status %d: fel %v, nytt försök %t, ville ha %t", status, err, retryable(err), wantRetry)
		}
	}
}

func TestHAPricesNotConfiguredIsPermanent(t *testing.T) {
	from, to := elprisetWindow()
	_, err := NewHAPriceService("", "", "", "SE3").FetchPrices(from, to)
	if !errors.Is(err, ErrMisconfigured) || retryable(err) {
		t.Errorf("utan HA_URL/HA_TOKEN ska felet vara permanent, fick %v", err)
	}
}
