package postgres

import "testing"

func TestURLFromEnvPrefersGongDatabaseURL(t *testing.T) {
	values := map[string]string{
		"GONG_DATABASE_URL": "postgres://writer/example",
		"DATABASE_URL":      "postgres://fallback/example",
	}
	got := URLFromEnv(func(key string) string { return values[key] })
	if got != "postgres://writer/example" {
		t.Fatalf("URLFromEnv()=%q want GONG_DATABASE_URL", got)
	}
}

func TestURLFromEnvFallsBackToDatabaseURL(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL": "postgres://fallback/example",
	}
	got := URLFromEnv(func(key string) string { return values[key] })
	if got != "postgres://fallback/example" {
		t.Fatalf("URLFromEnv()=%q want DATABASE_URL", got)
	}
}
