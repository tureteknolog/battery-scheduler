package services

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"battery-scheduler/models"
)

type EntsoeService struct {
	token   string
	area    string // SE1, SE2, SE3, SE4
	baseURL string
	client  *http.Client
}

// entsoeAPIURL är Entsoes Transparency Platform-endpoint
const entsoeAPIURL = "https://web-api.tp.entsoe.eu/api"

// ErrMissingToken returneras när Entsoe-token inte är konfigurerad
var ErrMissingToken = errors.New("Entsoe API token saknas - lägg till i settings")

// ErrNoData returneras när Entsoe svarar utan prisdata, t.ex. när morgondagens
// priser ännu inte är publicerade
var ErrNoData = errors.New("Entsoe svarade utan prisdata")

// HTTPStatusError är ett HTTP-fel från Entsoe med statuskoden bevarad, så att
// anroparen kan skilja tillfälliga fel (5xx, 429) från permanenta (t.ex. 401)
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	// Svaret kan vara en hel HTML-sida - t.ex. underhållssidan - så korta ner
	// den, annars dränker den både loggen och Pushover-notisen
	return fmt.Sprintf("Entsoe API error (status %d): %s", e.StatusCode, truncate(strings.TrimSpace(e.Body), 200))
}

// Retryable säger om felet är värt att försöka igen
func (e *HTTPStatusError) Retryable() bool {
	return e.StatusCode >= 500 || e.StatusCode == http.StatusTooManyRequests
}

// truncate kortar ner en sträng till max tecken (inte bytes, så flerbytetecken
// inte klipps mitt itu)
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// XML-strukturer för Entsoe API-svar
type EntsoeResponse struct {
	XMLName    xml.Name     `xml:"Publication_MarketDocument"`
	TimeSeries []TimeSeries `xml:"TimeSeries"`
}

type TimeSeries struct {
	Period Period `xml:"Period"`
}

type Period struct {
	TimeInterval TimeInterval `xml:"timeInterval"`
	Points       []Point      `xml:"Point"`
}

type TimeInterval struct {
	Start string `xml:"start"`
	End   string `xml:"end"`
}

type Point struct {
	Position int     `xml:"position"`
	Price    float64 `xml:"price.amount"`
}

// NewEntsoeService skapar en ny Entsoe-service
func NewEntsoeService(token, area string) *EntsoeService {
	return &EntsoeService{
		token:   token,
		area:    area,
		baseURL: entsoeAPIURL,
		// Timeout så att ett hängande anrop inte blockerar cron-jobbet i evighet
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// FetchPricesWithRetry hämtar priser och gör om försöket med dubblad fördröjning
// när felet är tillfälligt (5xx, 429, nätverksfel, ännu opublicerad data).
// Permanenta fel - saknad eller ogiltig token - avbryter direkt.
func (e *EntsoeService) FetchPricesWithRetry(from, to time.Time, attempts int, baseDelay time.Duration) ([]models.Price, error) {
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	delay := baseDelay

	for attempt := 1; attempt <= attempts; attempt++ {
		prices, err := e.FetchPrices(from, to)
		if err == nil {
			if attempt > 1 {
				log.Printf("Entsoe: prishämtningen lyckades på försök %d/%d", attempt, attempts)
			}
			return prices, nil
		}

		lastErr = err
		if !retryable(err) {
			return nil, fmt.Errorf("permanent fel från Entsoe: %w", err)
		}
		if attempt == attempts {
			break
		}

		log.Printf("Entsoe: försök %d/%d misslyckades (%v) - nytt försök om %s", attempt, attempts, err, delay)
		time.Sleep(delay)
		delay *= 2
	}

	return nil, fmt.Errorf("gav upp efter %d försök: %w", attempts, lastErr)
}

// retryable avgör om ett fel kan tänkas gå över av sig självt
func retryable(err error) bool {
	if errors.Is(err, ErrMissingToken) {
		return false
	}

	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.Retryable()
	}

	// Nätverksfel, avbrutna svar och ofullständiga dokument går ofta över
	return true
}

// FetchPrices hämtar kvartspriser från Entsoe för ett datumintervall
func (e *EntsoeService) FetchPrices(from, to time.Time) ([]models.Price, error) {
	if e.token == "" {
		return nil, ErrMissingToken
	}

	// Entsoe använder UTC
	fromUTC := from.UTC().Format("200601021504")
	toUTC := to.UTC().Format("200601021504")

	// Area codes för Sverige
	areaCodes := map[string]string{
		"SE1": "10Y1001A1001A44P", // Luleå
		"SE2": "10Y1001A1001A45N", // Sundsvall
		"SE3": "10Y1001A1001A46L", // Stockholm
		"SE4": "10Y1001A1001A47J", // Malmö
	}

	areaCode, ok := areaCodes[e.area]
	if !ok {
		return nil, fmt.Errorf("ogiltig prisområde: %s", e.area)
	}

	// Bygg URL
	url := fmt.Sprintf(
		e.baseURL+"?"+
			"securityToken=%s"+
			"&documentType=A44"+
			"&in_Domain=%s"+
			"&out_Domain=%s"+
			"&periodStart=%s"+
			"&periodEnd=%s",
		e.token, areaCode, areaCode, fromUTC, toUTC,
	)

	// Hämta data
	resp, err := e.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from Entsoe: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	// Parsa XML
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var entsoeResp EntsoeResponse
	if err := xml.Unmarshal(body, &entsoeResp); err != nil {
		return nil, fmt.Errorf("failed to parse XML: %w", err)
	}

	// Konvertera till vårt format
	var prices []models.Price

	for _, ts := range entsoeResp.TimeSeries {
		startTime, err := time.Parse("2006-01-02T15:04Z", ts.Period.TimeInterval.Start)
		if err != nil {
			continue
		}

		for _, point := range ts.Period.Points {
			// Position är 1-baserat (1-96 för kvartar)
			timestamp := startTime.Add(time.Duration(point.Position-1) * 15 * time.Minute)

			// Konvertera från EUR/MWh till öre/kWh inkl 25% moms
			// 1 EUR/MWh = 0.1 öre/kWh (ungefär, beroende på växelkurs)
			// Vi använder ca 11 SEK/EUR som standard
			// Korrekt konvertering: EUR/MWh -> öre/kWh inkl moms
			// 1. EUR/MWh -> EUR/kWh: /1000
			// 2. EUR -> SEK: *11 (växelkurs)
			// 3. SEK -> öre: *100
			// 4. Lägg till moms: *1.25
			// EUR/MWh -> öre/kWh inkl moms
			// Exempel: 14.18 EUR/MWh -> 0.01418 EUR/kWh -> 0.164 SEK/kWh -> 16.4 öre/kWh -> 20.5 öre/kWh
			const EXCHANGE_RATE = 11.6 // SEK per EUR (uppdatera efter behov)
			const VAT = 1.25           // 25% moms
			priceOreInclMoms := int(point.Price / 1000.0 * EXCHANGE_RATE * 100.0 * VAT)

			prices = append(prices, models.Price{
				Timestamp: timestamp.In(time.Local), // Konvertera till lokal tid
				PriceOre:  priceOreInclMoms,
				Area:      e.area,
			})
		}
	}

	// Ett svar utan prisdata (t.ex. ett Acknowledgement-dokument när morgondagens
	// priser inte är publicerade än) ska räknas som ett fel - annars sparas noll
	// priser och hämtningen ser ut att ha lyckats
	if len(prices) == 0 {
		return nil, ErrNoData
	}

	// Sort prices by timestamp and fill any gaps with interpolated values
	prices = e.fillPriceGaps(prices, from, to)

	return prices, nil
}

// fillPriceGaps sorts prices by timestamp and fills any missing 15-minute
// periods with interpolated values
func (e *EntsoeService) fillPriceGaps(prices []models.Price, from, to time.Time) []models.Price {
	if len(prices) == 0 {
		return prices
	}

	// Sort by timestamp
	sort.Slice(prices, func(i, j int) bool {
		return prices[i].Timestamp.Before(prices[j].Timestamp)
	})

	// Remove duplicates (keep first occurrence)
	seen := make(map[int64]bool)
	unique := make([]models.Price, 0, len(prices))
	for _, p := range prices {
		key := p.Timestamp.Unix()
		if !seen[key] {
			seen[key] = true
			unique = append(unique, p)
		}
	}
	prices = unique

	// Build a map of existing prices for quick lookup
	priceMap := make(map[int64]models.Price)
	for _, p := range prices {
		// Round to nearest 15-minute boundary
		ts := p.Timestamp.Truncate(15 * time.Minute)
		priceMap[ts.Unix()] = p
	}

	// Generate all expected 15-minute timestamps and fill gaps
	var result []models.Price
	fromLocal := from.In(time.Local).Truncate(15 * time.Minute)
	toLocal := to.In(time.Local).Truncate(15 * time.Minute)

	for current := fromLocal; current.Before(toLocal); current = current.Add(15 * time.Minute) {
		key := current.Unix()
		if existing, ok := priceMap[key]; ok {
			result = append(result, existing)
		} else {
			// Gap detected - interpolate from neighbors
			interpolated := e.interpolatePrice(current, priceMap)
			result = append(result, models.Price{
				Timestamp: current,
				PriceOre:  interpolated,
				Area:      e.area,
			})
		}
	}

	return result
}

// interpolatePrice finds the nearest prices before and after the gap
// and returns an interpolated value
func (e *EntsoeService) interpolatePrice(t time.Time, priceMap map[int64]models.Price) int {
	// Look for nearest price before
	var beforePrice, afterPrice int
	var foundBefore, foundAfter bool

	// Search backwards up to 4 hours (16 quarters)
	for delta := 15 * time.Minute; delta <= 4*time.Hour; delta += 15 * time.Minute {
		before := t.Add(-delta)
		if p, ok := priceMap[before.Unix()]; ok {
			beforePrice = p.PriceOre
			foundBefore = true
			break
		}
	}

	// Search forwards up to 4 hours (16 quarters)
	for delta := 15 * time.Minute; delta <= 4*time.Hour; delta += 15 * time.Minute {
		after := t.Add(delta)
		if p, ok := priceMap[after.Unix()]; ok {
			afterPrice = p.PriceOre
			foundAfter = true
			break
		}
	}

	// Interpolate based on what we found
	if foundBefore && foundAfter {
		return (beforePrice + afterPrice) / 2
	} else if foundBefore {
		return beforePrice
	} else if foundAfter {
		return afterPrice
	}

	// No neighbors found - use a default value
	return 100 // 100 öre as fallback
}

// GenerateMockPrices skapar mock-data för testning (används tills token finns)
func (e *EntsoeService) GenerateMockPrices(from, to time.Time) []models.Price {
	var prices []models.Price
	basePrice := 80

	current := from
	for current.Before(to) {
		hour := current.Hour()

		// Simulera daglig prisvariaton
		hourPrice := basePrice
		if hour >= 6 && hour <= 8 {
			hourPrice += 30 // Morgontopp
		} else if hour >= 17 && hour <= 20 {
			hourPrice += 40 // Kvällstopp
		} else if hour >= 0 && hour <= 4 {
			hourPrice -= 25 // Billig natt
		}

		// Lägg till lite slumpmässig variation
		// hourPrice += int((rand.Float64() - 0.5) * 20)

		// Lägg till moms (25%)
		priceInclMoms := int(float64(hourPrice) * 1.25)

		prices = append(prices, models.Price{
			Timestamp: current,
			PriceOre:  priceInclMoms,
			Area:      e.area,
		})

		current = current.Add(15 * time.Minute)
	}

	return prices
}
