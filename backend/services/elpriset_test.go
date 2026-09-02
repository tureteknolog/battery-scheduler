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

// dayFile bygger en dygnsfil i elprisetjustnu.se:s format med fyra kvartar
func dayFile(day time.Time, sek ...float64) string {
	var items []string
	for i, price := range sek {
		start := day.Add(time.Duration(i) * 15 * time.Minute)
		items = append(items, fmt.Sprintf(
			`{"SEK_per_kWh":%g,"EUR_per_kWh":0.1,"EXR":11.1,"time_start":"%s","time_end":"%s"}`,
			price, start.Format(time.RFC3339), start.Add(15*time.Minute).Format(time.RFC3339),
		))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func newTestElpriset(t *testing.T, handler http.HandlerFunc) (*ElprisetService, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	svc := NewElprisetService("SE3")
	svc.baseURL = server.URL
	return svc, server.Close
}

func elprisetWindow() (time.Time, time.Time) {
	from := time.Date(2026, 9, 2, 0, 0, 0, 0, time.Local)
	return from, from.Add(48 * time.Hour)
}

func TestElprisetFetchesBothDaysAndConvertsToOre(t *testing.T) {
	from, to := elprisetWindow()
	var paths []string
	svc, closeServer := newTestElpriset(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		day := from
		if strings.Contains(r.URL.Path, "09-03") {
			day = from.AddDate(0, 0, 1)
		}
		// 1.13873 SEK/kWh utan moms -> 142 öre inkl moms
		w.Write([]byte(dayFile(day, 1.13873, 0.95137)))
	})
	defer closeServer()

	prices, err := svc.FetchPrices(from, to)
	if err != nil {
		t.Fatalf("förväntade lyckad hämtning, fick: %v", err)
	}
	if len(prices) != 4 {
		t.Fatalf("förväntade 4 priser (två dygn a två kvartar), fick %d", len(prices))
	}
	if prices[0].PriceOre != 142 {
		t.Errorf("1.13873 SEK/kWh ska bli 142 öre inkl moms, blev %d", prices[0].PriceOre)
	}
	if prices[1].PriceOre != 118 {
		t.Errorf("0.95137 SEK/kWh ska bli 118 öre inkl moms, blev %d", prices[1].PriceOre)
	}
	if !prices[0].Timestamp.Equal(from) {
		t.Errorf("första kvarten ska vara %s, blev %s", from, prices[0].Timestamp)
	}
	if prices[0].Area != "SE3" {
		t.Errorf("elområdet följde inte med: %q", prices[0].Area)
	}
	if len(paths) != 2 || !strings.HasSuffix(paths[0], "/2026/09-02_SE3.json") || !strings.HasSuffix(paths[1], "/2026/09-03_SE3.json") {
		t.Errorf("fel dygnsfiler hämtades: %v", paths)
	}
}

func TestElprisetKeepsTheDaysItGotWhenTomorrowIsMissing(t *testing.T) {
	from, to := elprisetWindow()
	svc, closeServer := newTestElpriset(t, func(w http.ResponseWriter, r *http.Request) {
		// Morgondagens fil finns inte före kl 13
		if strings.Contains(r.URL.Path, "09-03") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(dayFile(from, 1.0)))
	})
	defer closeServer()

	prices, err := svc.FetchPrices(from, to)
	if err != nil {
		t.Fatalf("ett saknat dygn ska inte sänka hämtningen, fick: %v", err)
	}
	if len(prices) != 1 {
		t.Fatalf("förväntade dagens kvart, fick %d priser", len(prices))
	}
	// Kvartar utan pris ska lämnas utanför, inte fyllas i med gissningar
	for _, p := range prices {
		if p.Timestamp.After(from.AddDate(0, 0, 1)) {
			t.Errorf("morgondagen fylldes i trots att filen saknades: %s", p.Timestamp)
		}
	}
}

func TestElprisetErrorsWhenNothingIsPublished(t *testing.T) {
	from, to := elprisetWindow()
	svc, closeServer := newTestElpriset(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer closeServer()

	_, err := svc.FetchPrices(from, to)
	if err == nil {
		t.Fatal("förväntade fel när inget dygn gick att hämta")
	}
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Errorf("statuskoden bevarades inte: %v", err)
	}
}

func TestElprisetFiltersToWindow(t *testing.T) {
	from := time.Date(2026, 9, 2, 12, 0, 0, 0, time.Local)
	to := from.Add(30 * time.Minute)
	dayStart := time.Date(2026, 9, 2, 0, 0, 0, 0, time.Local)

	svc, closeServer := newTestElpriset(t, func(w http.ResponseWriter, r *http.Request) {
		// Ett helt dygn, men bara två kvartar ligger i fönstret
		var prices []float64
		for i := 0; i < 96; i++ {
			prices = append(prices, float64(i)/100)
		}
		w.Write([]byte(dayFile(dayStart, prices...)))
	})
	defer closeServer()

	prices, err := svc.FetchPrices(from, to)
	if err != nil {
		t.Fatalf("förväntade lyckad hämtning, fick: %v", err)
	}
	if len(prices) != 2 {
		t.Fatalf("förväntade 2 kvartar i fönstret 12:00-12:30, fick %d", len(prices))
	}
	if !prices[0].Timestamp.Equal(from) {
		t.Errorf("första kvarten ska vara 12:00, blev %s", prices[0].Timestamp)
	}
}
