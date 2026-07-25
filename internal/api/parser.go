package api

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

type StatEntry struct {
	Name    string `json:"name"`
	Percent string `json:"percent"`
}

type PokemonData struct {
	Abilities []StatEntry `json:"Abilities"`
	Items     []StatEntry `json:"Items"`
	Spreads   []StatEntry `json:"Spreads"`
	Moves     []StatEntry `json:"Moves"`
	Counters  []StatEntry `json:"Counters"`
	Teammates []StatEntry `json:"Teammates"`
}

var counterRegex = regexp.MustCompile(`^(.+?)\s+([0-9.]+)\s*\(`)

func ParseMoveset(text []byte) ([]byte, error) {
	str := strings.ReplaceAll(string(text), "\r\n", "\n")
	lines := strings.Split(str, "\n")
	data := make(map[string]*PokemonData)

	var currentPokemon string
	var currentSection string
	var pokemonData *PokemonData

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmedLine := strings.TrimSpace(line)

		if strings.HasPrefix(trimmedLine, "+----------------------------------------+") {
			if i+3 < len(lines) &&
				strings.HasPrefix(strings.TrimSpace(lines[i+2]), "+----------------------------------------+") &&
				strings.HasPrefix(strings.TrimSpace(lines[i+3]), "| Raw count:") {

				nameLine := strings.TrimSpace(lines[i+1])
				if len(nameLine) > 1 {
					idx := strings.Index(nameLine[1:], "|")
					if idx != -1 {
						currentPokemon = strings.TrimSpace(nameLine[1 : 1+idx])
						pokemonData = &PokemonData{
							Abilities: []StatEntry{},
							Items:     []StatEntry{},
							Spreads:   []StatEntry{},
							Moves:     []StatEntry{},
							Counters:  []StatEntry{},
							Teammates: []StatEntry{},
						}
						data[currentPokemon] = pokemonData
						currentSection = ""
						i += 3
						continue
					}
				}
			}
		}

		if currentPokemon == "" {
			continue
		}

		if strings.HasPrefix(trimmedLine, "+----------------------------------------+") {
			currentSection = ""
			continue
		}

		if strings.HasPrefix(trimmedLine, "| Abilities") {
			currentSection = "Abilities"
			continue
		}
		if strings.HasPrefix(trimmedLine, "| Items") {
			currentSection = "Items"
			continue
		}
		if strings.HasPrefix(trimmedLine, "| Spreads") {
			currentSection = "Spreads"
			continue
		}
		if strings.HasPrefix(trimmedLine, "| Moves") {
			currentSection = "Moves"
			continue
		}
		if strings.HasPrefix(trimmedLine, "| Teammates") {
			currentSection = "Teammates"
			continue
		}
		if strings.HasPrefix(trimmedLine, "| Checks and Counters") {
			currentSection = "Counters"
			continue
		}

		if currentSection != "" && pokemonData != nil {
			if strings.HasPrefix(trimmedLine, "| ") {
				content := trimmedLine[1:]
				// Remove trailing pipe and spaces
				content = strings.TrimRight(content, "| \t")
				content = strings.TrimSpace(content)

				if currentSection == "Counters" {
					if strings.HasPrefix(content, "(") {
						continue
					}
					match := counterRegex.FindStringSubmatch(content)
					if len(match) == 3 {
						name := strings.TrimSpace(match[1])
						percent := strings.TrimSpace(match[2])
						if name != "Other" && name != "Empty" {
							if p, err := strconv.ParseFloat(percent, 64); err == nil && p > 0 {
								pokemonData.Counters = append(pokemonData.Counters, StatEntry{Name: name, Percent: percent})
							}
						}
					}
				} else {
					lastSpace := strings.LastIndex(content, " ")
					if lastSpace != -1 {
						name := strings.TrimSpace(content[:lastSpace])
						percent := strings.TrimSpace(content[lastSpace+1:])
						percent = strings.TrimSuffix(percent, "%") // Strip % sign for ParseFloat
						if name != "Other" && name != "Empty" {
							if p, err := strconv.ParseFloat(percent, 64); err == nil && p > 0 {
								entry := StatEntry{Name: name, Percent: percent}
								switch currentSection {
								case "Abilities":
									pokemonData.Abilities = append(pokemonData.Abilities, entry)
								case "Items":
									pokemonData.Items = append(pokemonData.Items, entry)
								case "Spreads":
									pokemonData.Spreads = append(pokemonData.Spreads, entry)
								case "Moves":
									pokemonData.Moves = append(pokemonData.Moves, entry)
								case "Teammates":
									pokemonData.Teammates = append(pokemonData.Teammates, entry)
								}
							}
						}
					}
				}
			}
		}
	}

	return json.Marshal(data)
}
