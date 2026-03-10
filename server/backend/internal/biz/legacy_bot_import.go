package biz

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/config"
)

type LegacyBotImportResult struct {
	ImportedMappings int
	ImportedUsers    int
}

func ImportLegacyBotState() (*LegacyBotImportResult, error) {
	result := &LegacyBotImportResult{}

	platforms, err := loadLegacyPlatformMap(config.PlatformFile())
	if err != nil {
		return nil, err
	}
	mappings, err := loadLegacyBotUserMap(config.BotUserMapFile())
	if err != nil {
		return nil, err
	}
	for telegramID, canonicalUserID := range mappings {
		if err := UpsertLegacyTelegramUser(telegramID, canonicalUserID, platforms[telegramID], "ru"); err != nil {
			return nil, err
		}
		result.ImportedMappings++
	}

	registeredUsers, err := loadLegacyRegisteredUsers(config.DataFile())
	if err != nil {
		return nil, err
	}
	for _, telegramID := range registeredUsers {
		if _, exists := mappings[telegramID]; exists {
			continue
		}
		if _, err := EnsureTelegramUser(telegramID, platforms[telegramID], "ru"); err != nil {
			return nil, err
		}
		result.ImportedUsers++
	}

	return result, nil
}

func loadLegacyBotUserMap(path string) (map[int64]string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return map[int64]string{}, nil
		}
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[int64]string{}, nil
	}
	stringMap := map[string]string{}
	if err := json.Unmarshal(raw, &stringMap); err != nil {
		return nil, fmt.Errorf("parse legacy bot user map: %w", err)
	}
	result := make(map[int64]string, len(stringMap))
	for key, value := range stringMap {
		telegramID, err := strconv.ParseInt(strings.TrimSpace(key), 10, 64)
		if err != nil {
			continue
		}
		canonicalUserID := strings.TrimSpace(value)
		if canonicalUserID == "" {
			continue
		}
		result[telegramID] = canonicalUserID
	}
	return result, nil
}

func loadLegacyPlatformMap(path string) (map[int64]string, error) {
	return loadLegacyKeyValueFile(path)
}

func loadLegacyRegisteredUsers(path string) ([]int64, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var result []int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		id, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			continue
		}
		result = append(result, id)
	}
	return result, scanner.Err()
}

func loadLegacyKeyValueFile(path string) (map[int64]string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return map[int64]string{}, nil
		}
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := map[int64]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var key, value string
		switch {
		case strings.Contains(line, "="):
			parts := strings.SplitN(line, "=", 2)
			key, value = parts[0], parts[1]
		case strings.Contains(line, ":"):
			parts := strings.SplitN(line, ":", 2)
			key, value = parts[0], parts[1]
		case strings.Contains(line, ","):
			parts := strings.SplitN(line, ",", 2)
			key, value = parts[0], parts[1]
		default:
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			key, value = parts[0], parts[1]
		}
		telegramID, err := strconv.ParseInt(strings.TrimSpace(key), 10, 64)
		if err != nil {
			continue
		}
		result[telegramID] = strings.TrimSpace(value)
	}
	return result, scanner.Err()
}
