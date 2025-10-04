# Batteristyrning SE3

En komplett lösning för att styra hemma-batterilagring baserat på spotpriser från Nord Pool.

## Översikt

Detta system hjälper dig att:
- Schemalägga batteriladdning och urladdning baserat på elpriser
- Automatiskt hämta kvartspriser från Entsoe (SE3)
- Simulera batterinivå över tid
- Styra elbilsladdning
- Få push-notiser när nya priser är tillgängliga
- Exponera REST API för Home Assistant integration

## Teknisk stack

- **Backend:** Go 1.23, Gin web framework, SQLite
- **Frontend:** React 18, Tailwind CSS
- **Deployment:** Docker, Docker Compose
- **Integrationer:** Entsoe Transparency Platform, Pushover

## Krav

- Docker & Docker Compose
- Entsoe API token (registrera på https://transparency.entsoe.eu/)
- Pushover konto (valfritt, för notiser)

## Installation

### 1. Klona/skapa projektet

```bash
mkdir battery-scheduler
cd battery-scheduler
```

### 2. Bygg och starta

```bash
# Bygg Docker-imagen
docker-compose build

# Starta containern
docker-compose up -d

# Kolla loggar
docker-compose logs -f
```

### 3. Öppna webbgränssnittet

http://localhost:8080

## Konfiguration

### Första gången - Lägg in Pushover credentials

```bash
curl -X POST http://localhost:8080/api/settings \
  -H "Content-Type: application/json" \
  -d '{
    "pushover_app": "DIN_PUSHOVER_APP_TOKEN",
    "pushover_user": "DIN_PUSHOVER_USER_KEY",
    "app_url": "http://localhost:8080"
  }'
```

### När Entsoe-token kommer (tar upp till 24h)

```bash
curl -X POST http://localhost:8080/api/settings \
  -H "Content-Type: application/json" \
  -d '{
    "entsoe_token": "DIN_ENTSOE_TOKEN"
  }'
```

### Verifiera inställningar

```bash
curl http://localhost:8080/api/settings
```

## Docker-kommandon

```bash
# Starta
docker-compose up -d

# Stoppa
docker-compose down

# Starta om
docker-compose restart

# Se loggar
docker-compose logs -f

# Se loggar senaste 100 rader
docker-compose logs --tail=100

# Bygg om efter ändringar
docker-compose down
docker-compose build
docker-compose up -d

# Rensa allt (inkl databas!)
docker-compose down -v
```

## API Endpoints

### Priser
```bash
# Hämta alla priser (idag + imorgon)
GET http://localhost:8080/api/prices

# Tvinga uppdatering från Entsoe
POST http://localhost:8080/api/refresh-prices
```

### Schema
```bash
# Hämta aktuellt schema
GET http://localhost:8080/api/schedule

# Spara nytt schema
POST http://localhost:8080/api/schedule
Content-Type: application/json
[
  {"timestamp": "2025-10-04T02:00:00Z", "mode": 2},
  {"timestamp": "2025-10-04T06:00:00Z", "mode": 1}
]
```

### Aktuellt läge (för Home Assistant)
```bash
# Vilket läge är aktivt just nu?
GET http://localhost:8080/api/current-mode

# Exempel-svar:
{
  "mode": 2,
  "timestamp": "2025-10-04T14:30:00Z",
  "next_change": "2025-10-04T16:00:00Z",
  "next_mode": 1,
  "description": "Ladda från elnätet"
}
```

### Förbrukning & Batterinivå
```bash
# Gissad förbrukning per kvart
GET http://localhost:8080/api/power-estimate

# Aktuell batterinivå
GET http://localhost:8080/api/battery-soc
```

### Inställningar
```bash
# Hämta alla inställningar
GET http://localhost:8080/api/settings

# Spara inställningar
POST http://localhost:8080/api/settings
Content-Type: application/json
{
  "entsoe_token": "...",
  "pushover_app": "...",
  "pushover_user": "..."
}
```

### Health Check
```bash
GET http://localhost:8080/health
```

## Lägen (Modes)

1. **Passiv** - Använd endast solceller
2. **Ladda** - Ladda batteri från elnätet
3. **Urladda** - Urladda batteri till fastighet
4. **Effekt** - Effektbegränsning aktiv
5. **Laddbox Garage** - Elbilsladdning garage
6. **Laddbox Ute** - Elbilsladdning ute

**Regel:** När laddbox är aktiv måste batteriet vara i Passiv-läge.

## Automatisk prishämtning

Systemet hämtar automatiskt nya elpriser varje dag kl 13:05 (när Nord Pool släpper morgondagens priser).

En Pushover-notis skickas när priserna är hämtade.

## Databas

SQLite-databasen sparas i `./data/battery-scheduler.db` och överlever container-omstarter.

### Backup
```bash
# Kopiera databasen
cp data/battery-scheduler.db data/backup-$(date +%Y%m%d).db
```

### Återställ från backup
```bash
docker-compose down
cp data/backup-20251004.db data/battery-scheduler.db
docker-compose up -d
```

### Nollställ allt
```bash
docker-compose down
rm -rf data/
docker-compose up -d
```

## Utveckling

### Ändra frontend utan rebuild
Frontend-filer är monterade som volume, så du kan ändra `frontend/app.js` eller `frontend/index.html` och bara ladda om sidan i webbläsaren.

### Ändra backend
Efter ändringar i Go-kod:
```bash
docker-compose down
docker-compose build
docker-compose up -d
```

### Lokal utveckling (utan Docker)
```bash
# Terminal 1 - Backend
cd backend
go run main.go

# Terminal 2 - Frontend
# Öppna frontend/index.html i webbläsare
# eller använd en lokal webserver:
cd frontend
python3 -m http.server 3000
```

## Felsökning

### Containern startar inte
```bash
# Kolla loggar
docker-compose logs

# Kolla om port 8080 är upptagen
lsof -i :8080
```

### Inga priser visas
- Kontrollera att Entsoe-token är korrekt
- Kolla loggar: `docker-compose logs -f`
- Tvinga uppdatering: `curl -X POST http://localhost:8080/api/refresh-prices`

### Pushover-notiser fungerar inte
- Verifiera credentials: `curl http://localhost:8080/api/settings`
- Testa manuellt: `curl -X POST http://localhost:8080/api/refresh-prices`

### Databasen är korrupt
```bash
docker-compose down
rm data/battery-scheduler.db
docker-compose up -d
```

## Home Assistant Integration

Exempel på REST sensor i `configuration.yaml`:

```yaml
sensor:
  - platform: rest
    name: Battery Mode
    resource: http://localhost:8080/api/current-mode
    value_template: "{{ value_json.mode }}"
    json_attributes:
      - description
      - next_change
      - next_mode
    scan_interval: 60
```

## Proxmox Deployment

För att köra i Proxmox:

1. Skapa en LXC container med Docker
2. Klona projektet
3. Kör `docker-compose up -d`
4. Sätt upp reverse proxy (Nginx/Traefik) för extern åtkomst

## Säkerhet

- **VIKTIGT:** Detta system har ingen autentisering
- Exponera INTE port 8080 direkt på internet
- Använd reverse proxy med SSL/TLS för extern åtkomst
- Överväg att lägga till basic auth i Nginx/Traefik

## Licens

MIT

## Support

För buggar och feature requests, skapa ett issue på GitHub (när du pushat projektet).