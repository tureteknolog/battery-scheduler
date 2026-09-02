package main

import (
	"fmt"
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
	// Logga vilken tidszon processen faktiskt kör i. Saknas tzdata i imagen
	// faller Go tillbaka på UTC trots TZ-variabeln, och då blir dygnsgränserna
	// (och därmed pris- och schemafönstret) förskjutna.
	log.Printf("Local timezone: %s (TZ=%q, now=%s)", time.Local, os.Getenv("TZ"), time.Now().Format(time.RFC3339))

	// Läs miljövariabler först, fallback till databas
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/battery-scheduler.db" // Persistent plats
	}

	// Skapa databas
	database, err := db.NewDatabase(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	log.Println("Database initialized")

	// Hämta inställningar från miljövariabler först, sedan databas
	entsoeToken := os.Getenv("ENTSOE_TOKEN")
	if entsoeToken == "" {
		entsoeToken, _ = database.GetSetting("entsoe_token")
	}

	pushoverApp := os.Getenv("PUSHOVER_APP")
	if pushoverApp == "" {
		pushoverApp, _ = database.GetSetting("pushover_app")
	}

	pushoverUser := os.Getenv("PUSHOVER_USER")
	if pushoverUser == "" {
		pushoverUser, _ = database.GetSetting("pushover_user")
	}

	area := os.Getenv("PRICE_AREA")
	if area == "" {
		area = "SE3"
	}

	// Home Assistant-inställningar
	haURL := os.Getenv("HA_URL")
	if haURL == "" {
		haURL, _ = database.GetSetting("ha_url")
	}
	haToken := os.Getenv("HA_TOKEN")
	if haToken == "" {
		haToken, _ = database.GetSetting("ha_token")
	}

	// SMHI-koordinater (default: Nacka/Stockholm)
	smhiLat := 59.38309
	smhiLon := 17.01550

	// Skapa services
	entsoeService := services.NewEntsoeService(entsoeToken, area)
	// Reservkälla när Entsoes API ligger nere. Entsoe först - det är originalet,
	// med vår egen växelkurs - och elprisetjustnu.se bara när det inte går.
	elprisetService := services.NewElprisetService(area)
	priceSources := []services.PriceSource{entsoeService, elprisetService}
	pushoverService := services.NewPushoverService(pushoverApp, pushoverUser)
	smhiService := services.NewSMHIService(smhiLat, smhiLon)
	haService := services.NewHomeAssistantService(haURL, haToken)

	// Skapa API
	apiHandler := api.NewAPI(database, priceSources, pushoverService, smhiService, haService)

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
		runScheduledPriceFetch(database, priceSources, pushoverService)
	})

	c.Start()
	log.Printf("Cron scheduler started (price fetch at 13:05 daily, up to %d attempts)", priceFetchAttempts)

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

const (
	// Entsoe kan vara nere för underhåll när priserna släpps, så försök igen med
	// 5, 10, 20 och 40 minuters mellanrum - drygt en timme totalt, vilket ger god
	// marginal till kvällens dyra timmar.
	priceFetchAttempts  = 5
	priceFetchBaseDelay = 5 * time.Minute
)

// runScheduledPriceFetch hämtar morgondagens priser, sparar dem och notifierar
// via Pushover - både när det gick bra och när det inte gjorde det.
func runScheduledPriceFetch(database *db.Database, priceSources []services.PriceSource, pushover *services.PushoverService) {
	log.Println("Running scheduled price fetch...")

	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfTomorrow := startOfToday.Add(48 * time.Hour)

	appURL, _ := database.GetSetting("app_url")

	notifyFailure := func(summary string, cause error) {
		log.Printf("%s %v", summary, cause)
		if err := pushover.SendPriceFetchFailedNotification(summary, cause, appURL); err != nil {
			log.Printf("Failed to send Pushover failure notification: %v", err)
		} else {
			log.Println("Pushover failure notification sent")
		}
	}

	prices, source, err := services.FetchPricesWithRetry(priceSources, startOfToday, endOfTomorrow, priceFetchAttempts, priceFetchBaseDelay)
	if err != nil {
		notifyFailure(fmt.Sprintf("Kunde inte hämta elpriser efter %d försök - varken från Entsoe eller reservkällan.", priceFetchAttempts), err)
		return // Avbryt - spara INTE mock-data till databasen
	}

	if err := database.SavePrices(prices); err != nil {
		notifyFailure("Priserna hämtades men kunde inte sparas i databasen.", err)
		return
	}

	log.Printf("Successfully fetched and saved %d prices from %s", len(prices), source)

	// Beräkna statistik
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
	if err := pushover.SendPriceUpdateNotification(avg, min, max, source, appURL); err != nil {
		log.Printf("Failed to send Pushover notification: %v", err)
	} else {
		log.Println("Pushover notification sent successfully")
	}
}
