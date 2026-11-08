package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"battery-scheduler/models"
)

// DefaultHAPriceEntity är Nord Pool-sensorn i Home Assistant: SE3, SEK, två
// decimaler, moms 25 % inräknad
const DefaultHAPriceEntity = "sensor.nordpool_kwh_se3_sek_2_10_025"

// ErrMisconfigured är ett fel i inställningarna: inget att vänta ut
var ErrMisconfigured = errors.New("felaktigt konfigurerad priskälla")

// HAPriceService läser kvartspriserna ur Nord Pool-integrationen i Home Assistant.
//
// Det är den enda källan som inte går via Entsoe: integrationen hämtar direkt
// från Nord Pool. elprisetjustnu.se hämtar själv från Entsoe och faller därför
// samtidigt (Entsoe gav inga priser 2026-11-05--08).
//
// Sensorn har dagens och morgondagens kvartar som attributen raw_today och
// raw_tomorrow. Priset är i SEK/kWh inklusive moms, men utan elskatt och
// nätavgift -- samma sak som de andra källorna ger. Precis som för
// elprisetjustnu.se fylls inga luckor i.
type HAPriceService struct {
	baseURL string
	token   string
	entity  string
	area    string
	client  *http.Client
}

// haPrice är en kvart i raw_today/raw_tomorrow. value är null om Nord Pool
// saknar pris för kvarten.
type haPrice struct {
	Start string   `json:"start"`
	Value *float64 `json:"value"`
}

type haPriceState struct {
	Attributes struct {
		Region        string    `json:"region"`
		Currency      string    `json:"currency"`
		RawToday      []haPrice `json:"raw_today"`
		RawTomorrow   []haPrice `json:"raw_tomorrow"`
		TomorrowValid bool      `json:"tomorrow_valid"`
	} `json:"attributes"`
}

// NewHAPriceService skapar priskällan. Tom entity ger DefaultHAPriceEntity.
func NewHAPriceService(baseURL, token, entity, area string) *HAPriceService {
	if entity == "" {
		entity = DefaultHAPriceEntity
	}
	return &HAPriceService{
		baseURL: baseURL,
		token:   token,
		entity:  entity,
		area:    area,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Name är källans namn, för logg och notis
func (h *HAPriceService) Name() string { return "Home Assistant (Nord Pool)" }

// FetchPrices hämtar kvartspriser för [from, to)
func (h *HAPriceService) FetchPrices(from, to time.Time) ([]models.Price, error) {
	if h.baseURL == "" || h.token == "" {
		return nil, fmt.Errorf("HA_URL eller HA_TOKEN saknas: %w", ErrMisconfigured)
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/states/%s", h.baseURL, h.entity), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch from Home Assistant: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var state haPriceState
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Fel sensor ger fel priser utan att något annat märks
	a := state.Attributes
	if a.Region != h.area || a.Currency != "SEK" {
		return nil, fmt.Errorf("%s gäller %s i %s, inte %s i SEK: %w", h.entity, a.Region, a.Currency, h.area, ErrMisconfigured)
	}

	quarters := a.RawToday
	// Före publiceringen vid 13-tiden kan raw_tomorrow innehålla gamla eller
	// tomma värden; tomorrow_valid säger om de gäller
	if a.TomorrowValid {
		quarters = append(quarters, a.RawTomorrow...)
	}

	fromLocal := from.In(time.Local)
	toLocal := to.In(time.Local)

	var prices []models.Price
	for _, q := range quarters {
		if q.Value == nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, q.Start)
		if err != nil {
			continue
		}
		ts = ts.In(time.Local)
		if ts.Before(fromLocal) || !ts.Before(toLocal) {
			continue
		}
		prices = append(prices, models.Price{
			// SEK/kWh inkl moms -> öre/kWh inkl moms. Två decimaler i sensorn,
			// så avrundningen är exakt.
			Timestamp: ts,
			PriceOre:  int(math.Round(*q.Value * 100)),
			Area:      h.area,
		})
	}

	if len(prices) == 0 {
		return nil, ErrNoData
	}
	return prices, nil
}
