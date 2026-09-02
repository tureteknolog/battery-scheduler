package services

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"battery-scheduler/models"
)

// elprisetAPIURL är elprisetjustnu.se:s öppna API - en JSON-fil per dygn och elområde
const elprisetAPIURL = "https://www.elprisetjustnu.se/api/v1/prices"

// ElprisetService hämtar priser från elprisetjustnu.se och används som reserv när
// Entsoe inte svarar.
//
// Observera att de själva hämtar sin data från Entsoe, så källan hjälper när det
// är Entsoes API som ligger nere - inte om Entsoe skulle sluta publicera data i
// botten. Priserna räknas dessutom om med deras växelkurs (exchangeratesapi.io)
// i stället för vår fasta kurs, så de kan skilja sig någon procent från våra
// Entsoe-priser. Variationerna över dygnet - det schemat faktiskt styr på - är
// desamma.
type ElprisetService struct {
	area    string
	baseURL string
	client  *http.Client
}

// elprisetPrice är en post i dygnsfilen. Priset är i SEK/kWh exklusive moms.
type elprisetPrice struct {
	SEKPerKWh float64 `json:"SEK_per_kWh"`
	TimeStart string  `json:"time_start"`
}

// NewElprisetService skapar en ny reservpriskälla
func NewElprisetService(area string) *ElprisetService {
	return &ElprisetService{
		area:    area,
		baseURL: elprisetAPIURL,
		// Samma skäl som för Entsoe: ett hängande anrop får inte blockera cron-jobbet
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Name är källans namn, för logg och notis
func (e *ElprisetService) Name() string { return "elprisetjustnu.se" }

// FetchPrices hämtar kvartspriser för [from, to). API:t har en fil per dygn, så
// vi hämtar varje dygn som fönstret rör vid och klipper till.
//
// Till skillnad från Entsoe-hämtningen fylls inga luckor i: kvartar vi inte fått
// pris för lämnas utanför, så att gränssnittet kan visa dem som okända i stället
// för att gissa.
func (e *ElprisetService) FetchPrices(from, to time.Time) ([]models.Price, error) {
	switch e.area {
	case "SE1", "SE2", "SE3", "SE4":
	default:
		return nil, fmt.Errorf("ogiltig prisområde: %s", e.area)
	}

	const VAT = 1.25 // 25% moms - deras priser är utan moms, precis som Entsoes

	fromLocal := from.In(time.Local)
	toLocal := to.In(time.Local)

	var prices []models.Price
	var lastErr error

	day := time.Date(fromLocal.Year(), fromLocal.Month(), fromLocal.Day(), 0, 0, 0, 0, time.Local)
	for ; day.Before(toLocal); day = day.AddDate(0, 0, 1) {
		dayPrices, err := e.fetchDay(day)
		if err != nil {
			// Ett dygn som saknas ska inte sänka de andra - morgondagens fil
			// finns t.ex. inte före kl 13
			lastErr = err
			log.Printf("elprisetjustnu.se: %s saknas (%v)", day.Format("2006-01-02"), err)
			continue
		}

		for _, p := range dayPrices {
			ts, err := time.Parse(time.RFC3339, p.TimeStart)
			if err != nil {
				continue
			}
			ts = ts.In(time.Local)
			if ts.Before(fromLocal) || !ts.Before(toLocal) {
				continue
			}

			prices = append(prices, models.Price{
				// SEK/kWh -> öre/kWh inkl moms, samma enhet som Entsoe-priserna
				Timestamp: ts,
				PriceOre:  int(p.SEKPerKWh * 100.0 * VAT),
				Area:      e.area,
			})
		}
	}

	if len(prices) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, ErrNoData
	}

	return prices, nil
}

// fetchDay hämtar ett dygns prisfil
func (e *ElprisetService) fetchDay(day time.Time) ([]elprisetPrice, error) {
	url := fmt.Sprintf("%s/%d/%02d-%02d_%s.json", e.baseURL, day.Year(), int(day.Month()), day.Day(), e.area)

	resp, err := e.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from elprisetjustnu.se: %w", err)
	}
	defer resp.Body.Close()

	// Läs aldrig in mer än vad ett dygn rimligen kan vara, oavsett vad vi får
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		// 404 = dygnet är inte publicerat än, hanteras som alla andra fel: vi
		// tar de dygn vi fick och låter resten vara okända
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var dayPrices []elprisetPrice
	if err := json.Unmarshal(body, &dayPrices); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}
	if len(dayPrices) == 0 {
		return nil, ErrNoData
	}

	return dayPrices, nil
}
