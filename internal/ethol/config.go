package ethol

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Username                string
	Password                string
	TelegramToken           string
	TelegramChatID          string
	TelegramCommandThreadID int64
	TelegramNotifThreadID   int64
	AutoPresence            bool
	TelegramAllowedUserIDs  []int64
	HealthAddr              string
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func parseEnv(data []byte) map[string]string {
	env := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		env[key] = val
	}
	return env
}

func resolveValue(override, key string, fileEnv map[string]string) string {
	if override != "" {
		return override
	}
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fileEnv[key]
}

func parseConfigInt64(s, key string) (int64, error) {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return v, nil
}

func LoadConfig(path string, overrides ...Config) (*Config, error) {
	env := make(map[string]string)
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		if err == nil {
			env = parseEnv(data)
		}
	}

	var override Config
	if len(overrides) > 0 {
		override = overrides[0]
	}
	commandThread, err := resolveConfigInt64(override.TelegramCommandThreadID, "TELEGRAM_COMMAND_THREAD_ID", env)
	if err != nil {
		return nil, err
	}
	notifThread, err := resolveConfigInt64(override.TelegramNotifThreadID, "TELEGRAM_NOTIF_THREAD_ID", env)
	if err != nil {
		return nil, err
	}
	allowedUsers, err := resolveUserIDs("TELEGRAM_ALLOWED_USER_IDS", env)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Username:                resolveValue(override.Username, "ETHOL_EMAIL", env),
		Password:                resolveValue(override.Password, "ETHOL_PASSWORD", env),
		TelegramToken:           resolveValue(override.TelegramToken, "TELEGRAM_TOKEN", env),
		TelegramChatID:          resolveValue(override.TelegramChatID, "TELEGRAM_CHAT_ID", env),
		TelegramCommandThreadID: commandThread,
		TelegramNotifThreadID:   notifThread,
		AutoPresence:            override.AutoPresence || parseBool(resolveValue("", "ETHOL_AUTO_PRESENCE", env)),
		TelegramAllowedUserIDs:  allowedUsers,
		HealthAddr:              resolveValue(override.HealthAddr, "ETHOL_HEALTH_ADDR", env),
	}

	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("ETHOL_EMAIL and ETHOL_PASSWORD are required in config")
	}

	return cfg, nil
}

func resolveUserIDs(key string, fileEnv map[string]string) ([]int64, error) {
	value := os.Getenv(key)
	if value == "" {
		value = fileEnv[key]
	}
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := parseConfigInt64(part, key)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("%s contains invalid user ID", key)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func resolveConfigInt64(override int64, key string, fileEnv map[string]string) (int64, error) {
	if override != 0 {
		return override, nil
	}
	value := os.Getenv(key)
	if value == "" {
		value = fileEnv[key]
	}
	if value == "" {
		return 0, nil
	}
	return parseConfigInt64(value, key)
}
