package api

import (
	"bufio"
	"bytes"
	"github.com/goccy/go-json"
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

func ParseMoveset(text []byte) ([]byte, error) {
	data := make(map[string]*PokemonData)
	scanner := bufio.NewScanner(bytes.NewReader(text))

	var currentPokemon string
	var currentSection string
	var pokemonData *PokemonData
	var state int
	var tempName string

	for scanner.Scan() {
		line := scanner.Bytes()
		trimmedLine := bytes.TrimSpace(line)

		if bytes.HasPrefix(trimmedLine, []byte("+----------------------------------------+")) {
			if state == 2 {
				state = 3
			} else {
				state = 1
			}
			currentSection = ""
			continue
		}

		if bytes.HasPrefix(trimmedLine, []byte("| Abilities")) {
			currentSection = "Abilities"
			state = 0
			continue
		}
		if bytes.HasPrefix(trimmedLine, []byte("| Items")) {
			currentSection = "Items"
			state = 0
			continue
		}
		if bytes.HasPrefix(trimmedLine, []byte("| Spreads")) {
			currentSection = "Spreads"
			state = 0
			continue
		}
		if bytes.HasPrefix(trimmedLine, []byte("| Moves")) {
			currentSection = "Moves"
			state = 0
			continue
		}
		if bytes.HasPrefix(trimmedLine, []byte("| Teammates")) {
			currentSection = "Teammates"
			state = 0
			continue
		}
		if bytes.HasPrefix(trimmedLine, []byte("| Checks and Counters")) {
			currentSection = "Counters"
			state = 0
			continue
		}

		if state == 1 {
			if len(trimmedLine) > 1 && trimmedLine[0] == '|' {
				idx := bytes.Index(trimmedLine[1:], []byte("|"))
				if idx != -1 {
					tempName = string(bytes.TrimSpace(trimmedLine[1 : 1+idx]))
					state = 2
					continue
				}
			}
			state = 0
		} else if state == 3 {
			if bytes.HasPrefix(trimmedLine, []byte("| Raw count:")) {
				currentPokemon = tempName
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
				state = 0
				continue
			}
			state = 0
		}

		if currentPokemon == "" {
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
					idxParen := strings.LastIndexByte(content, '(')
					if idxParen != -1 {
						beforeParen := strings.TrimSpace(content[:idxParen])
						lastSpace := strings.LastIndexByte(beforeParen, ' ')
						if lastSpace != -1 {
							name := strings.TrimSpace(beforeParen[:lastSpace])
							percent := strings.TrimSpace(beforeParen[lastSpace+1:])
							if name != "Other" && name != "Empty" {
								if p, err := strconv.ParseFloat(percent, 64); err == nil && p > 0 {
									pokemonData.Counters = append(pokemonData.Counters, StatEntry{Name: name, Percent: percent})
								}
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
