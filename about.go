package main

// Version is set at build time via -ldflags "-X main.Version=X.Y.Z"
var Version = "0.0.1-rc2"

// Tagline is the project tribute displayed in the About section.
var Tagline = "Born from the chaos of the NZ1U Barnstormers Contest Group and the spirit of " +
	"Dick Pechie, KB1H (SK) — who started this crazy ride and somehow kept us all on frequency."

// Contributor represents a project contributor.
type Contributor struct {
	Callsign string `json:"callsign"`
	Name     string `json:"name,omitempty"`
	Role     string `json:"role,omitempty"`
}

// Contributors lists the people who helped make this project happen.
var Contributors = []Contributor{
	{Callsign: "KB1H (SK)", Name: "Dick Pechie", Role: "Main Inspiration"},
	{Callsign: "W1UJ", Name: "Jay C", Role: "The Dude, visionary and developer"},
	{Callsign: "K1EBY", Name: "Frank", Role: "Inspiration and patriarch"},
	{Callsign: "N1WK", Name: "Kim", Role: "BoBo Wrangler and Chief Station Engineer"},
	{Callsign: "NB1U", Name: "Mark", Role: "Inspiration"},
	{Callsign: "NR1X", Name: "Allan", Role: "Inspiration"},
	{Callsign: "K1DW", Name: "Dallas", Role: "Inspiration"},
	{Callsign: "AA1CE", Name: "Dave", Role: "Inspiration"},
	{Callsign: "W4WUT", Name: "MK", Role: "Inspiration"},
	{Callsign: "Anton P.", Name: "Anton", Role: "Inspiration and may be licensed someday :)"},
}

// AboutInfo is the JSON payload served by /api/about.
type AboutInfo struct {
	Version      string        `json:"version"`
	Tagline      string        `json:"tagline"`
	Contributors []Contributor `json:"contributors"`
}

// GetAboutInfo returns the full about info for the application.
func GetAboutInfo() AboutInfo {
	return AboutInfo{
		Version:      Version,
		Tagline:      Tagline,
		Contributors: Contributors,
	}
}
