package api

import (
	"bytes"
	"github.com/goccy/go-json"
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
	text = bytes.ReplaceAll(text, []byte("\r\n"), []byte("\n"))
	lines := bytes.Split(text, []byte("\n"))
	data := make(map[string]*PokemonData)

	var currentPokemon string
	var currentSection string
	var pokemonData *PokemonData

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmedLine := bytes.TrimSpace(line)

		if bytes.HasPrefix(trimmedLine, []byte("+----------------------------------------+")) {
			if i+3 < len(lines) &&
				bytes.HasPrefix(bytes.TrimSpace(lines[i+2]), []byte("+----------------------------------------+")) &&
				bytes.HasPrefix(bytes.TrimSpace(lines[i+3]), []byte("| Raw count:")) {

				nameLine := bytes.TrimSpace(lines[i+1])
				if len(nameLine) > 1 {
					idx := bytes.Index(nameLine[1:], []byte("|"))
					if idx != -1 {
						currentPokemon = string(bytes.TrimSpace(nameLine[1 : 1+idx]))
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

		if bytes.HasPrefix(trimmedLine, []byte("+----------------------------------------+")) {
			currentSection = ""
			continue
		}

		if bytes.HasPrefix(trimmedLine, []byte("| Abilities")) {
			currentSection = "Abilities"
			continue
		}
		if bytes.HasPrefix(trimmedLine, []byte("| Items")) {
			currentSection = "Items"
			continue
		}
		if bytes.HasPrefix(trimmedLine, []byte("| Spreads")) {
			currentSection = "Spreads"
			continue
		}
		if bytes.HasPrefix(trimmedLine, []byte("| Moves")) {
			currentSection = "Moves"
			continue
		}
		if bytes.HasPrefix(trimmedLine, []byte("| Teammates")) {
			currentSection = "Teammates"
			continue
		}
		if bytes.HasPrefix(trimmedLine, []byte("| Checks and Counters")) {
			currentSection = "Counters"
			continue
		}

		if currentSection != "" && pokemonData != nil {
			if bytes.HasPrefix(trimmedLine, []byte("| ")) {
				content := string(trimmedLine[1:])
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
						percent = strings.TrimSuffix(percent, "%")
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

	return json.MarshalNoEscape(data)
}
