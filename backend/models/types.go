package models

import "time"

// Price representerar ett elpris för ett kvart
type Price struct {
	Timestamp time.Time `json:"timestamp"`
	PriceOre  int       `json:"price"` // Pris i öre inkl moms
	Area      string    `json:"area"`  // SE1, SE2, SE3, SE4
}

// ScheduleChange representerar en ändring i schemat (en breakpoint)
type ScheduleChange struct {
	ID        int       `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Mode      int       `json:"mode"` // 1-6
	CreatedAt time.Time `json:"created_at"`
}

// PowerEstimate är gissad förbrukning för ett kvart
type PowerEstimate struct {
	Timestamp time.Time `json:"timestamp"`
	PowerKW   float64   `json:"power_kw"`
}

// CurrentModeResponse är vad vi returnerar till Home Assistant
type CurrentModeResponse struct {
	Mode        int       `json:"mode"`
	Timestamp   time.Time `json:"timestamp"`
	NextChange  time.Time `json:"next_change,omitempty"`
	NextMode    int       `json:"next_mode,omitempty"`
	Description string    `json:"description"`
}

// BatterySoCResponse är aktuellt laddtillstånd
type BatterySoCResponse struct {
	Percentage float64   `json:"percentage"`
	Timestamp  time.Time `json:"timestamp"`
}

// Settings representerar en nyckel-värde-inställning
type Setting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Mode-beskrivningar
var ModeDescriptions = map[int]string{
	1: "Passiv (endast solceller)",
	2: "Ladda från elnätet",
	3: "Urladda till fastighet",
	4: "Effektbegränsning aktiv",
	5: "Laddbox Garage aktiv",
	6: "Laddbox Ute aktiv",
}
