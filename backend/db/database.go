package db

import (
	"database/sql"
	"fmt"
	"time"

	"battery-scheduler/models"

	_ "github.com/mattn/go-sqlite3"
)

type Database struct {
	db *sql.DB
}

// NewDatabase skapar eller öppnar databasen
func NewDatabase(dbPath string) (*Database, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Testa anslutningen
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	database := &Database{db: db}

	// Skapa tabeller om de inte finns
	if err := database.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return database, nil
}

// migrate skapar alla nödvändiga tabeller
func (d *Database) migrate() error {
	schema := `
    CREATE TABLE IF NOT EXISTS prices (
        timestamp DATETIME PRIMARY KEY,
        price_ore INTEGER NOT NULL,
        area TEXT DEFAULT 'SE3'
    );

    CREATE TABLE IF NOT EXISTS schedule (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        timestamp DATETIME NOT NULL,
        mode INTEGER NOT NULL,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );

    CREATE TABLE IF NOT EXISTS power_estimates (
        timestamp DATETIME PRIMARY KEY,
        power_kw REAL NOT NULL
    );

    CREATE TABLE IF NOT EXISTS settings (
        key TEXT PRIMARY KEY,
        value TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS history (
        timestamp DATETIME PRIMARY KEY,
        mode INTEGER,
        battery_soc REAL,
        power_kw REAL,
        price_ore INTEGER
    );

    CREATE INDEX IF NOT EXISTS idx_schedule_timestamp ON schedule(timestamp);
    CREATE INDEX IF NOT EXISTS idx_prices_timestamp ON prices(timestamp);
    `

	_, err := d.db.Exec(schema)
	return err
}

// SavePrices sparar en batch av priser
func (d *Database) SavePrices(prices []models.Price) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT OR REPLACE INTO prices (timestamp, price_ore, area) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range prices {
		_, err := stmt.Exec(p.Timestamp, p.PriceOre, p.Area)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetPrices hämtar priser för ett tidsintervall
func (d *Database) GetPrices(from, to time.Time) ([]models.Price, error) {
	rows, err := d.db.Query(
		"SELECT timestamp, price_ore, area FROM prices WHERE timestamp >= ? AND timestamp < ? ORDER BY timestamp",
		from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prices []models.Price
	for rows.Next() {
		var p models.Price
		if err := rows.Scan(&p.Timestamp, &p.PriceOre, &p.Area); err != nil {
			return nil, err
		}
		prices = append(prices, p)
	}

	return prices, rows.Err()
}

// SaveSchedule sparar ett helt nytt schema (ersätter gammalt)
func (d *Database) SaveSchedule(changes []models.ScheduleChange) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Ta bort gammalt schema
	_, err = tx.Exec("DELETE FROM schedule")
	if err != nil {
		return err
	}

	// Lägg till nya breakpoints
	stmt, err := tx.Prepare("INSERT INTO schedule (timestamp, mode) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, change := range changes {
		_, err := stmt.Exec(change.Timestamp, change.Mode)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetSchedule hämtar alla schemaändringar
func (d *Database) GetSchedule() ([]models.ScheduleChange, error) {
	rows, err := d.db.Query(
		"SELECT id, timestamp, mode, created_at FROM schedule ORDER BY timestamp",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var changes []models.ScheduleChange
	for rows.Next() {
		var c models.ScheduleChange
		if err := rows.Scan(&c.ID, &c.Timestamp, &c.Mode, &c.CreatedAt); err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}

	return changes, rows.Err()
}

// GetSetting hämtar en inställning
func (d *Database) GetSetting(key string) (string, error) {
	var value string
	err := d.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SaveSetting sparar en inställning
func (d *Database) SaveSetting(key, value string) error {
	_, err := d.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value)
	return err
}

// Close stänger databasanslutningen
func (d *Database) Close() error {
	return d.db.Close()
}
