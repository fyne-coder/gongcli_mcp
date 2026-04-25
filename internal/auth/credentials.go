package auth

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Credentials struct {
	AccessKey       string
	AccessKeySecret string
}

var dotEnvKeys = map[string]bool{
	"GONG_ACCESS_KEY":        true,
	"GONG_ACCESS_KEY_SECRET": true,
	"GONG_BASE_URL":          true,
}

func LoadFromEnv() (Credentials, error) {
	if err := ApplyDotEnv(".env"); err != nil {
		return Credentials{}, err
	}

	creds := Credentials{
		AccessKey:       os.Getenv("GONG_ACCESS_KEY"),
		AccessKeySecret: os.Getenv("GONG_ACCESS_KEY_SECRET"),
	}
	if creds.AccessKey == "" {
		return creds, errors.New("missing GONG_ACCESS_KEY")
	}
	if creds.AccessKeySecret == "" {
		return creds, errors.New("missing GONG_ACCESS_KEY_SECRET")
	}
	return creds, nil
}

func (c Credentials) Empty() bool {
	return c.AccessKey == "" || c.AccessKeySecret == ""
}

func ApplyDotEnv(path string) error {
	values, err := ReadDotEnv(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	for key, value := range values {
		if !dotEnvKeys[key] {
			continue
		}
		if os.Getenv(key) == "" {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func ReadDotEnv(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, lineNumber)
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty key", path, lineNumber)
		}
		if strings.HasPrefix(key, "#") {
			continue
		}

		values[key] = unquote(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	first := value[0]
	last := value[len(value)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}
