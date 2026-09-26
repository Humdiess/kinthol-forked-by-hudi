package ethol

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
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

type ServiceConfig struct {
	Addr              string
	StatePath         string
	AdminToken        string
	EncryptionKey     string
	BaseURL           string
	WorkerConcurrency int
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
		env[key] = parseEnvValue(strings.TrimSpace(parts[1]))
	}
	return env
}

// parseEnvValue handles quoted values with escaped characters and strips inline
// comments from unquoted values.
func parseEnvValue(raw string) string {
	if raw == "" {
		return ""
	}
	if raw[0] == '"' || raw[0] == '\'' {
		quote := raw[0]
		// Fast path: quoted value with no escape sequence returns a substring.
		if end := strings.IndexByte(raw[1:], quote); end >= 0 && !strings.ContainsRune(raw[1:1+end], '\\') {
			return raw[1 : 1+end]
		}
		var b strings.Builder
		for i := 1; i < len(raw); i++ {
			if raw[i] == '\\' && i+1 < len(raw) {
				b.WriteByte(raw[i+1])
				i++
				continue
			}
			if raw[i] == quote {
				return b.String()
			}
			b.WriteByte(raw[i])
		}
		return b.String()
	}
	for i := 1; i < len(raw); i++ {
		if raw[i] == '#' && (raw[i-1] == ' ' || raw[i-1] == '\t') {
			raw = raw[:i]
			break
		}
	}
	return strings.TrimSpace(raw)
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

	func parsePositiveInt(s, key string) (int, error) {
		v, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer", key)
		}
		return v, nil
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
	if (cfg.TelegramToken == "") != (cfg.TelegramChatID == "") {
		return nil, errors.New("TELEGRAM_TOKEN and TELEGRAM_CHAT_ID must be configured together")
	}
	if cfg.TelegramChatID != "" {
		if _, err := strconv.ParseInt(strings.TrimSpace(cfg.TelegramChatID), 10, 64); err != nil {
			return nil, errors.New("TELEGRAM_CHAT_ID must be an integer")
		}
	}
	if strings.HasPrefix(strings.TrimSpace(cfg.TelegramChatID), "-") && len(allowedUsers) == 0 {
		return nil, errors.New("TELEGRAM_ALLOWED_USER_IDS is required for group chats")
	}
	if cfg.HealthAddr != "" {
		if _, _, err := net.SplitHostPort(cfg.HealthAddr); err != nil {
			return nil, fmt.Errorf("ETHOL_HEALTH_ADDR must be host:port: %w", err)
		}
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

	func LoadServiceConfig(path string, override ServiceConfig) (*ServiceConfig, error) {
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

		cfg := &ServiceConfig{
			Addr:          resolveValue(override.Addr, "ETHOL_SERVICE_ADDR", env),
			StatePath:     resolveValue(override.StatePath, "ETHOL_SERVICE_STATE_PATH", env),
			AdminToken:    resolveValue(override.AdminToken, "ETHOL_SERVICE_ADMIN_TOKEN", env),
			EncryptionKey: resolveValue(override.EncryptionKey, "ETHOL_SERVICE_ENCRYPTION_KEY", env),
			BaseURL:       resolveValue(override.BaseURL, "ETHOL_SERVICE_BASE_URL", env),
		}
		if cfg.StatePath == "" {
			cfg.StatePath = "ethold_service_state.json"
		}
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://ethol.pens.ac.id"
		}

		concurrency := override.WorkerConcurrency
		if concurrency == 0 {
			value := os.Getenv("ETHOL_SERVICE_WORKER_CONCURRENCY")
			if value == "" {
				value = env["ETHOL_SERVICE_WORKER_CONCURRENCY"]
			}
			if value == "" {
				concurrency = 4
			} else {
				v, err := parsePositiveInt(value, "ETHOL_SERVICE_WORKER_CONCURRENCY")
				if err != nil {
					return nil, err
				}
				concurrency = v
			}
		}
		cfg.WorkerConcurrency = concurrency

		if strings.TrimSpace(cfg.Addr) == "" {
			return nil, errors.New("ETHOL_SERVICE_ADDR is required in service mode")
		}
		if strings.TrimSpace(cfg.AdminToken) == "" {
			return nil, errors.New("ETHOL_SERVICE_ADMIN_TOKEN is required in service mode")
		}
		if strings.TrimSpace(cfg.EncryptionKey) == "" {
			return nil, errors.New("ETHOL_SERVICE_ENCRYPTION_KEY is required in service mode")
		}

		return cfg, nil
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
