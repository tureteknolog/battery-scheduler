package main

import (
	"log"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"

	"battery-scheduler/api"
	"battery-scheduler/db"
	"battery-scheduler/services"
)

func main() {
	// Skapa databas
	database, err := db.NewDatabase("battery-scheduler.db")
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	log.Println("Database initialized")

	// Hämta inställningar från databas
	entsoeToken, _ := database.GetSetting("entsoe_token")
	pushoverApp, _ := database.GetSetting("pushover_app")
	pushoverUser, _ := database.GetSetting("pushover_user")
	area := "SE3" // Kan göras konfigurerbart senare

	// Skapa services
	entsoeService := services.NewEntsoeService(entsoeToken, area)
	pushoverService := services.NewPushoverService(pushoverApp, pushoverUser)

	// Skapa API
	apiHandler := api.NewAPI(database, entsoeService, pushoverService)

	// Sätt upp Gin router
	router := gin.Default()

	// CORS middleware för att frontend ska kunna prata med backend
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	// API routes
	apiRoutes := router.Group("/api")
	{
		apiRoutes.GET("/prices", apiHandler.GetPrices)
		apiRoutes.GET("/schedule", apiHandler.GetSchedule)
		apiRoutes.POST("/schedule", apiHandler.SaveSchedule)
		apiRoutes.GET("/current-mode", apiHandler.GetCurrentMode)
		apiRoutes.GET("/power-estimate", apiHandler.GetPowerEstimate)
		apiRoutes.GET("/battery-soc", apiHandler.GetBatterySoC)
		apiRoutes.POST("/refresh-prices", apiHandler.RefreshPrices)
		apiRoutes.GET("/settings", apiHandler.GetSettings)
		apiRoutes.POST("/settings", apiHandler.SaveSettings)
	}

	// Servera frontend (statiska filer)
	router.Static("/frontend", "./frontend")
	router.GET("/", func(c *gin.Context) {
		c.Redirect(302, "/frontend/index.html")
	})

	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Sätt upp cron för automatisk prishämtning
	c := cron.New()

	// Kör varje dag kl 13:05 (när Nordpool släpper morgondagens priser)
	c.AddFunc("5 13 * * *", func() {
		log.Println("Running scheduled price fetch...")

		now := time.Now()
		startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		endOfTomorrow := startOfToday.Add(48 * time.Hour)

		prices, err := entsoeService.FetchPrices(startOfToday, endOfTomorrow)
		if err != nil {
			log.Printf("Failed to fetch prices from Entsoe: %v", err)
			// Fallback till mock-data
			prices = entsoeService.GenerateMockPrices(startOfToday, endOfTomorrow)
		}

		if err := database.SavePrices(prices); err != nil {
			log.Printf("Failed to save prices: %v", err)
			return
		}

		log.Printf("Successfully fetched and saved %d prices", len(prices))

		// Beräkna statistik
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
			appURL, _ := database.GetSetting("app_url")
			if err := pushoverService.SendPriceUpdateNotification(avg, min, max, appURL); err != nil {
				log.Printf("Failed to send Pushover notification: %v", err)
			} else {
				log.Println("Pushover notification sent successfully")
			}
		}
	})

	c.Start()
	log.Println("Cron scheduler started (price fetch at 13:05 daily)")

	// Starta servern
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting server on port %s", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
