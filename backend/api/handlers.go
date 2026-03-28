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
	db            *db.Database
	entsoe        *services.EntsoeService
	pushover      *services.PushoverService
	scheduler     *services.SchedulerService
	smhi          *services.SMHIService
	homeAssistant *services.HomeAssistantService
}

// NewAPI skapar en ny API-instans
func NewAPI(database *db.Database, entsoe *services.EntsoeService, pushover *services.PushoverService, smhi *services.SMHIService, ha *services.HomeAssistantService) *API {
	// Ladda befintligt schema från databasen
	schedule, _ := database.GetSchedule()
	scheduler := services.NewSchedulerService(schedule)

	return &API{
		db:            database,
		entsoe:        entsoe,
		pushover:      pushover,
		scheduler:     scheduler,
		smhi:          smhi,
		homeAssistant: ha,
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

// GetPowerEstimate returnerar prognosticerad förbrukning per kvart baserat på SMHI-temperatur
func (a *API) GetPowerEstimate(c *gin.Context) {
	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// Hämta väderprognos från SMHI
	forecasts, err := a.smhi.FetchForecast()
	if err != nil {
		fmt.Printf("Failed to fetch SMHI forecast, using fallback: %v\n", err)
	}

	var estimates []models.PowerEstimate

	for i := 0; i < 192; i++ { // 48 timmar * 4 kvartar
		timestamp := startOfToday.Add(time.Duration(i) * 15 * time.Minute)

		var power float64
		if len(forecasts) > 0 {
			temp := a.smhi.GetTemperatureAt(forecasts, timestamp)
			power = services.ConsumptionFromTemperature(temp)
		} else {
			// Fallback: anta 5°C
			power = services.ConsumptionFromTemperature(5.0)
		}

		estimates = append(estimates, models.PowerEstimate{
			Timestamp:   timestamp,
			PowerKW:     power,
			Temperature: a.getTemperatureForTimestamp(forecasts, timestamp),
		})
	}

	c.JSON(http.StatusOK, estimates)
}

// getTemperatureForTimestamp returnerar temperaturen vid en given tidpunkt, eller 0 om prognos saknas
func (a *API) getTemperatureForTimestamp(forecasts []services.TemperatureForecast, t time.Time) *float64 {
	if len(forecasts) == 0 {
		return nil
	}
	temp := a.smhi.GetTemperatureAt(forecasts, t)
	return &temp
}

// GetBatterySoC returnerar aktuellt batteriladdtillstånd från Home Assistant
func (a *API) GetBatterySoC(c *gin.Context) {
	soc, lastChanged, err := a.homeAssistant.GetSoC()
	if err != nil {
		fmt.Printf("Failed to fetch SoC from Home Assistant: %v\n", err)
		// Fallback till mock-värde
		c.JSON(http.StatusOK, models.BatterySoCResponse{
			Percentage: 50.0,
			Timestamp:  time.Now(),
		})
		return
	}

	c.JSON(http.StatusOK, models.BatterySoCResponse{
		Percentage: soc,
		Timestamp:  lastChanged,
	})
}

// RefreshPrices hämtar nya priser från Entsoe
func (a *API) RefreshPrices(c *gin.Context) {
	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfTomorrow := startOfToday.Add(48 * time.Hour)

	// Hämta priser från Entsoe
	prices, err := a.entsoe.FetchPrices(startOfToday, endOfTomorrow)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Kunde inte hämta priser: %v", err)})
		return
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
		"ha_url":           "",
		"ha_token":         "",
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
