package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"battery-scheduler/db"
	"battery-scheduler/models"
	"battery-scheduler/services"
)

type API struct {
	db        *db.Database
	entsoe    *services.EntsoeService
	pushover  *services.PushoverService
	scheduler *services.SchedulerService
}

// NewAPI skapar en ny API-instans
func NewAPI(database *db.Database, entsoe *services.EntsoeService, pushover *services.PushoverService) *API {
	// Ladda befintligt schema från databasen
	schedule, _ := database.GetSchedule()
	scheduler := services.NewSchedulerService(schedule)

	return &API{
		db:        database,
		entsoe:    entsoe,
		pushover:  pushover,
		scheduler: scheduler,
	}
}

// GetPrices returnerar priser för idag och imorgon (192 kvartar)
func (a *API) GetPrices(c *gin.Context) {
	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfTomorrow := startOfToday.Add(48 * time.Hour)

	prices, err := a.db.GetPrices(startOfToday, endOfTomorrow)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Om inga priser finns i databasen, använd mock-data
	if len(prices) == 0 {
		prices = a.entsoe.GenerateMockPrices(startOfToday, endOfTomorrow)
	}

	c.JSON(http.StatusOK, prices)
}

// GetSchedule returnerar aktuellt schema
func (a *API) GetSchedule(c *gin.Context) {
	schedule, err := a.db.GetSchedule()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, schedule)
}

// SaveSchedule sparar ett nytt schema
func (a *API) SaveSchedule(c *gin.Context) {
	var schedule []models.ScheduleChange

	if err := c.ShouldBindJSON(&schedule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON format"})
		return
	}

	// Validera schemat
	tempScheduler := services.NewSchedulerService(schedule)
	if err := tempScheduler.ValidateSchedule(schedule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Spara till databas
	if err := a.db.SaveSchedule(schedule); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Uppdatera scheduler
	a.scheduler.UpdateSchedule(schedule)

	c.JSON(http.StatusOK, gin.H{"message": "Schema sparat"})
}

// GetCurrentMode returnerar vilket läge som är aktivt just nu (för Home Assistant)
func (a *API) GetCurrentMode(c *gin.Context) {
	now := time.Now()
	currentMode := a.scheduler.GetCurrentMode(now)

	c.JSON(http.StatusOK, currentMode)
}

// GetPowerEstimate returnerar gissad förbrukning per kvart
func (a *API) GetPowerEstimate(c *gin.Context) {
	// TODO: Implementera riktig förbrukningsgissning senare
	// För nu returnerar vi mock-data

	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var estimates []models.PowerEstimate

	for i := 0; i < 192; i++ { // 48 timmar * 4 kvartar
		timestamp := startOfToday.Add(time.Duration(i) * 15 * time.Minute)
		hour := timestamp.Hour()

		var power float64
		if hour >= 0 && hour < 6 {
			power = 0.5 + float64(i%4)*0.1 // Natt: 0.5-0.8 kW
		} else if hour >= 6 && hour < 9 {
			power = 2.0 + float64(i%4)*0.25 // Morgon: 2-3 kW
		} else if hour >= 9 && hour < 17 {
			power = 1.0 + float64(i%4)*0.2 // Dag: 1-2 kW
		} else if hour >= 17 && hour < 22 {
			power = 3.0 + float64(i%4)*0.5 // Kväll: 3-5 kW
		} else {
			power = 1.0 + float64(i%4)*0.2 // Sen kväll: 1-2 kW
		}

		estimates = append(estimates, models.PowerEstimate{
			Timestamp: timestamp,
			PowerKW:   power,
		})
	}

	c.JSON(http.StatusOK, estimates)
}

// GetBatterySoC returnerar aktuellt batteriladdtillstånd
func (a *API) GetBatterySoC(c *gin.Context) {
	// TODO: Hämta riktigt värde från Ferroamp/Home Assistant senare
	// För nu returnerar vi mock-data

	response := models.BatterySoCResponse{
		Percentage: 50.0, // Mock-värde
		Timestamp:  time.Now(),
	}

	c.JSON(http.StatusOK, response)
}

// RefreshPrices hämtar nya priser från Entsoe
func (a *API) RefreshPrices(c *gin.Context) {
	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfTomorrow := startOfToday.Add(48 * time.Hour)

	// Hämta priser från Entsoe
	prices, err := a.entsoe.FetchPrices(startOfToday, endOfTomorrow)
	if err != nil {
		// Om det misslyckas (t.ex. saknas token), använd mock-data
		prices = a.entsoe.GenerateMockPrices(startOfToday, endOfTomorrow)
	}

	// Spara till databas
	if err := a.db.SavePrices(prices); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Beräkna statistik för notifikation
	if len(prices) > 0 {
		var sum, min, max int
		min = prices[0].PriceOre
		max = prices[0].PriceOre

		for _, p := range prices {
			sum += p.PriceOre
			if p.PriceOre < min {
				min = p.PriceOre
			}
			if p.PriceOre > max {
				max = p.PriceOre
			}
		}

		avg := sum / len(prices)

		// Skicka Pushover-notis
		appURL, _ := a.db.GetSetting("app_url")
		if err := a.pushover.SendPriceUpdateNotification(avg, min, max, appURL); err != nil {
			// Logga fel men fortsätt ändå
			fmt.Printf("Failed to send Pushover notification: %v\n", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Hämtade %d priser", len(prices)),
		"prices":  len(prices),
	})
}

// GetSettings returnerar alla inställningar
func (a *API) GetSettings(c *gin.Context) {
	settings := map[string]string{
		"entsoe_token":     "",
		"pushover_app":     "",
		"pushover_user":    "",
		"app_url":          "",
		"battery_capacity": "42",
	}

	// Hämta faktiska värden från databas
	for key := range settings {
		if value, err := a.db.GetSetting(key); err == nil && value != "" {
			settings[key] = value
		}
	}

	c.JSON(http.StatusOK, settings)
}

// SaveSettings sparar inställningar
func (a *API) SaveSettings(c *gin.Context) {
	var settings map[string]string

	if err := c.ShouldBindJSON(&settings); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON format"})
		return
	}

	// Spara varje inställning
	for key, value := range settings {
		if err := a.db.SaveSetting(key, value); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Inställningar sparade"})
}
