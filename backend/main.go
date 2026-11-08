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
	// Reservkällor när Entsoes API ligger nere. Entsoe först - det är originalet,
	// med vår egen växelkurs. Sedan Nord Pool via Home Assistant, den enda som
	// inte själv hämtar från Entsoe, och sist elprisetjustnu.se.
	haPriceService := services.NewHAPriceService(haURL, haToken, os.Getenv("HA_PRICE_ENTITY"), area)
	elprisetService := services.NewElprisetService(area)
	priceSources := []services.PriceSource{entsoeService, haPriceService, elprisetService}
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

	// Cron kör i time.Local, så tiden nedan är lokal och följer sommar-/vintertid
	// av sig själv. (Innan tzdata kom in i imagen var time.Local UTC, och då sköt
	// samma uttryck fram hämtningen till 15:05 på sommaren och 14:05 på vintern.)
	fetchPrices := func() {
		runScheduledPriceFetch(database, priceSources, pushoverService)
	}

	spec := os.Getenv("PRICE_FETCH_CRON")
	if spec == "" {
		spec = defaultPriceFetchCron
	}
	if _, err := c.AddFunc(spec, fetchPrices); err != nil {
		log.Printf("Ogiltigt PRICE_FETCH_CRON %q (%v) - använder %q i stället", spec, err, defaultPriceFetchCron)
		spec = defaultPriceFetchCron
		if _, err := c.AddFunc(spec, fetchPrices); err != nil {
			log.Fatalf("Failed to schedule price fetch: %v", err)
		}
	}

	c.Start()
	log.Printf("Cron scheduler started (price fetch at %q local time, up to %d attempts)", spec, priceFetchAttempts)

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

	// Morgondagens priser släpps vid 13-tiden, men Entsoe har dem sällan uppe på
	// slaget - 14:05 lokal tid ger en timmes marginal. Kan sättas om med
	// PRICE_FETCH_CRON (standard cron-syntax, lokal tid) utan att bygga om koden.
	defaultPriceFetchCron = "5 14 * * *"
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
		notifyFailure(fmt.Sprintf("Kunde inte hämta elpriser efter %d försök - varken från Entsoe eller reservkällorna.", priceFetchAttempts), err)
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
