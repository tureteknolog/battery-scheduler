package services

import (
	"errors"
	"fmt"
	"log"
	"time"

	"battery-scheduler/models"
)

// PriceSource är en källa att hämta dygnspriser från
type PriceSource interface {
	// Name är källans namn, för logg och notis
	Name() string
	FetchPrices(from, to time.Time) ([]models.Price, error)
}

// FetchPricesWithRetry provar källorna i den ordning de kommer och gör om hela
// varvet med dubblad fördröjning så länge felen är tillfälliga (5xx, 429,
// nätverksfel, ännu opublicerad data). Returnerar namnet på källan som svarade,
// så att loggen och notisen kan säga var priserna kom ifrån.
//
// Reservkällan provas direkt efter Entsoe i varje varv, inte först när alla
// försök är slut: ligger Entsoe nere i dagar är det ingen idé att vänta ut hela
// backoffen innan vi frågar någon annan.
func FetchPricesWithRetry(sources []PriceSource, from, to time.Time, attempts int, baseDelay time.Duration) ([]models.Price, string, error) {
	if len(sources) == 0 {
		return nil, "", errors.New("ingen priskälla konfigurerad")
	}
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	delay := baseDelay

	for attempt := 1; attempt <= attempts; attempt++ {
		// Går alla källor på permanenta fel - t.ex. saknad token - finns inget
		// att vänta på, då avbryter vi i stället för att sova bort en timme
		anyRetryable := false

		for _, source := range sources {
			prices, err := source.FetchPrices(from, to)
			if err == nil {
				log.Printf("Prishämtning: %d priser från %s (försök %d/%d)", len(prices), source.Name(), attempt, attempts)
				return prices, source.Name(), nil
			}

			lastErr = fmt.Errorf("%s: %w", source.Name(), err)
			if retryable(err) {
				anyRetryable = true
			}
			log.Printf("Prishämtning: %s misslyckades (försök %d/%d): %v", source.Name(), attempt, attempts, err)
		}

		if !anyRetryable {
			return nil, "", fmt.Errorf("permanent fel: %w", lastErr)
		}
		if attempt == attempts {
			break
		}

		log.Printf("Prishämtning: nytt försök om %s", delay)
		time.Sleep(delay)
		delay *= 2
	}

	return nil, "", fmt.Errorf("gav upp efter %d försök: %w", attempts, lastErr)
}
