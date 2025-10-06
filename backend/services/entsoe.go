package services

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"

	"battery-scheduler/models"
)

type EntsoeService struct {
	token string
	area  string // SE1, SE2, SE3, SE4
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
		token: token,
		area:  area,
	}
}

// FetchPrices hämtar kvartspriser från Entsoe för ett datumintervall
func (e *EntsoeService) FetchPrices(from, to time.Time) ([]models.Price, error) {
	if e.token == "" {
		return nil, fmt.Errorf("Entsoe API token saknas - lägg till i settings")
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
		"https://web-api.tp.entsoe.eu/api?"+
			"securityToken=%s"+
			"&documentType=A44"+
			"&in_Domain=%s"+
			"&out_Domain=%s"+
			"&periodStart=%s"+
			"&periodEnd=%s",
		e.token, areaCode, areaCode, fromUTC, toUTC,
	)

	// Hämta data
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from Entsoe: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Entsoe API error (status %d): %s", resp.StatusCode, string(body))
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
			const EXCHANGE_RATE = 11.0
			const VAT = 1.25
			priceOreInclMoms := int(point.Price / 1000.0 * EXCHANGE_RATE * 100.0 * VAT)

			prices = append(prices, models.Price{
				Timestamp: timestamp.In(time.Local), // Konvertera till lokal tid
				PriceOre:  priceOreInclMoms,
				Area:      e.area,
			})
		}
	}

	return prices, nil
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
