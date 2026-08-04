package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: populate-all <usage|leads|format-stats|metagame|viability|all>")
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "usage":
		populateUsage()
	case "leads":
		populateLeads()
	case "format-stats":
		populateFormatStats()
	case "metagame":
		populateMetagame()
	case "viability":
		populateViability()
	case "all":
		populateUsage()
		populateFormatStats()
		populateLeads()
		populateMetagame()
		populateViability()
	default:
		fmt.Println("Unknown command:", cmd)
		os.Exit(1)
	}
}
