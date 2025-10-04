package services

import (
	"fmt"
	"time"

	"battery-scheduler/models"
)

type SchedulerService struct {
	schedule []models.ScheduleChange
}

// NewSchedulerService skapar en ny scheduler
func NewSchedulerService(schedule []models.ScheduleChange) *SchedulerService {
	return &SchedulerService{
		schedule: schedule,
	}
}

// UpdateSchedule uppdaterar schemat (anropas efter SaveSchedule)
func (s *SchedulerService) UpdateSchedule(schedule []models.ScheduleChange) {
	s.schedule = schedule
}

// GetCurrentMode returnerar vilket läge som är aktivt just nu
func (s *SchedulerService) GetCurrentMode(now time.Time) models.CurrentModeResponse {
	if len(s.schedule) == 0 {
		// Inget schema - default är Passiv (läge 1)
		return models.CurrentModeResponse{
			Mode:        1,
			Timestamp:   now,
			Description: models.ModeDescriptions[1],
		}
	}

	// Hitta senaste breakpoint före eller vid "now"
	var currentMode int = 1 // Default
	var nextChange time.Time
	var nextMode int

	for i, change := range s.schedule {
		if change.Timestamp.After(now) {
			// Detta är nästa ändring
			nextChange = change.Timestamp
			nextMode = change.Mode
			break
		}
		// Detta är den senaste breakpoint som passerat
		currentMode = change.Mode

		// Kolla om det finns en nästa ändring
		if i+1 < len(s.schedule) {
			nextChange = s.schedule[i+1].Timestamp
			nextMode = s.schedule[i+1].Mode
		}
	}

	response := models.CurrentModeResponse{
		Mode:        currentMode,
		Timestamp:   now,
		Description: models.ModeDescriptions[currentMode],
	}

	// Lägg till nästa ändring om den finns
	if !nextChange.IsZero() {
		response.NextChange = nextChange
		response.NextMode = nextMode
	}

	return response
}

// GetModeForTime returnerar vilket läge som gäller vid en specifik tidpunkt
func (s *SchedulerService) GetModeForTime(t time.Time) int {
	if len(s.schedule) == 0 {
		return 1 // Default Passiv
	}

	currentMode := 1
	for _, change := range s.schedule {
		if change.Timestamp.After(t) {
			break
		}
		currentMode = change.Mode
	}

	return currentMode
}

// ValidateSchedule kontrollerar att schemat är giltigt
func (s *SchedulerService) ValidateSchedule(schedule []models.ScheduleChange) error {
	// Kontrollera att laddboxar inte överlappar
	chargerActive := make(map[time.Time]int) // timestamp -> mode (5 eller 6)

	for _, change := range schedule {
		if change.Mode == 5 || change.Mode == 6 {
			// Hitta när denna laddbox slutar (nästa breakpoint)
			var endTime time.Time
			for _, nextChange := range schedule {
				if nextChange.Timestamp.After(change.Timestamp) {
					endTime = nextChange.Timestamp
					break
				}
			}

			// Om ingen slutpunkt finns, fortsätter den till slutet
			if endTime.IsZero() {
				endTime = change.Timestamp.Add(48 * time.Hour) // Arbiträrt långt
			}

			// Kontrollera överlappning med andra laddboxar
			for checkTime := change.Timestamp; checkTime.Before(endTime); checkTime = checkTime.Add(15 * time.Minute) {
				if existingMode, exists := chargerActive[checkTime]; exists {
					if existingMode == 5 || existingMode == 6 {
						// Det finns redan en laddbox aktiv vid denna tid
						return fmt.Errorf("laddboxar överlappar vid %s", checkTime.Format("2006-01-02 15:04"))
					}
				}
				chargerActive[checkTime] = change.Mode
			}
		}
	}

	return nil
}
