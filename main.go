package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	baseURL = "https://www.robotevents.com/api/v2/events/%s/teams?&per_page=250&page=1"
)

// Team represents an individual team in the response
type Team struct {
	ID           int    `json:"id"`
	Number       string `json:"number"`
	TeamName     string `json:"team_name"`
	RobotName    string `json:"robot_name"`
	Organization string `json:"organization"`
	Location     struct {
		City     string `json:"city"`
		Region   string `json:"region"`
		Country  string `json:"country"`
		Postcode string `json:"postcode"`
	} `json:"location"`
	Registered bool `json:"registered"`
}

// APIResponse represents the overall structure of the API response
type APIResponse struct {
	Meta struct {
		Total int `json:"total"`
	} `json:"meta"`
	Data []Team `json:"data"`
}

// fetchTeams fetches the teams for a given event ID and parses the response
func fetchTeams(token, eventID string) (*APIResponse, error) {
	url := fmt.Sprintf(baseURL, eventID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func fetchEventName(token, eventID string) (string, error) {
	url := fmt.Sprintf("https://www.robotevents.com/api/v2/events/%s", eventID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Add("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Name, nil
}

// loadPreviousTeams reads teams from a saved file specific to an event
func loadPreviousTeams(eventID string) (*APIResponse, error) {
	fileName := fmt.Sprintf("%s_teams.json", eventID)
	data, err := ioutil.ReadFile(fileName)
	if err != nil {
		return nil, err
	}

	var prevTeams APIResponse
	if err := json.Unmarshal(data, &prevTeams); err != nil {
		return nil, err
	}
	return &prevTeams, nil
}

// saveTeams saves the current teams data to a file specific to an event
func saveTeams(eventID string, teams *APIResponse) error {
	fileName := fmt.Sprintf("%s_teams.json", eventID)
	data, err := json.MarshalIndent(teams, "", "  ")
	if err != nil {
		return err
	}

	return ioutil.WriteFile(fileName, data, 0644)
}

// findMissingTeams compares two slices of teams and finds any missing in the current data
func findMissingTeams(prevTeams, currTeams []Team) []Team {
	currMap := make(map[int]Team)
	for _, team := range currTeams {
		currMap[team.ID] = team
	}

	var missingTeams []Team
	for _, team := range prevTeams {
		if _, exists := currMap[team.ID]; !exists {
			missingTeams = append(missingTeams, team)
		}
	}
	return missingTeams
}

// sendSlackMessage sends a message with the missing teams to Slack
func sendSlackMessage(webhookURL, eventName string, missingTeams []Team) error {
	var message string
	if len(missingTeams) == 0 {
		message = fmt.Sprintf("No teams are missing for event %s.", eventName)
	} else {
		message = fmt.Sprintf("*Missing teams for* `%s`:\n\n", eventName)
		for _, team := range missingTeams {
			message += fmt.Sprintf("`%s` - `%s`\n", team.Number, team.Organization)
		}
	}

	payload := map[string]string{"text": message}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to send Slack message, status code: %d", resp.StatusCode)
	}
	return nil
}

func main() {
	// Get the token and webhook URL from the environment
	token := os.Getenv("API_TOKEN")
	if token == "" {
		log.Fatal("API_TOKEN environment variable is not set")
	}
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	if webhookURL == "" {
		log.Fatal("SLACK_WEBHOOK_URL environment variable is not set")
	}

	// List of event IDs to process
	eventIDs := strings.Split(os.Getenv("EVENT_IDS"), ",")

	for _, eventID := range eventIDs {
		fmt.Printf("Processing event ID: %s\n", eventID)

		// Fetch current teams for the event from API
		currTeams, err := fetchTeams(token, eventID)
		if err != nil {
			log.Printf("Error fetching teams for event %s: %v\n", eventID, err)
			continue
		}

		// Load previous teams from file
		prevTeams, err := loadPreviousTeams(eventID)
		if err != nil && !os.IsNotExist(err) {
			log.Printf("Error reading previous teams file for event %s: %v\n", eventID, err)
			continue
		}

		// Save the current teams to a file
		if err := saveTeams(eventID, currTeams); err != nil {
			log.Printf("Error saving teams to file for event %s: %v\n", eventID, err)
			continue
		}

		// Check for missing teams if previous data exists
		if prevTeams != nil {
			missingTeams := findMissingTeams(prevTeams.Data, []Team{}) //currTeams.Data)
			if len(missingTeams) > 0 {
				eventName, err := fetchEventName(token, eventID)
				if err != nil {
					log.Printf("Error retrieving name of event %s: %v\n", eventID, err)
					continue
				}

				if err := sendSlackMessage(webhookURL, eventName, missingTeams); err != nil {
					log.Printf("Error sending Slack message for event %s: %v\n", eventID, err)
				}
			}
		} else {
			fmt.Printf("No previous team data to compare for event %s.\n", eventID)
		}
	}
}
