package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

func loadItems(path string) ([]Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// detect jsonl vs json array
	if strings.HasSuffix(strings.ToLower(path), ".jsonl") {
		var items []Item
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var it Item
			if err := json.Unmarshal([]byte(line), &it); err != nil {
				return nil, err
			}
			// basic validation
			if it.PDFURL == "" {
				continue
			}
			items = append(items, it)
		}
		return items, sc.Err()
	}

	// else treat as JSON array
	var items []Item
	if err := json.NewDecoder(f).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}
