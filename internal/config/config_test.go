package config

import (
	"log/slog"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "defaults",
			env:  map[string]string{"DATABASE_URL": "postgres://db"},
			want: Config{DatabaseURL: "postgres://db", HTTPAddr: ":8080", LogLevel: slog.LevelInfo},
		},
		{
			name: "overrides",
			env:  map[string]string{"DATABASE_URL": "postgres://db", "HTTP_ADDR": ":9000", "LOG_LEVEL": "debug"},
			want: Config{DatabaseURL: "postgres://db", HTTPAddr: ":9000", LogLevel: slog.LevelDebug},
		},
		{
			name:    "missing DATABASE_URL",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name:    "invalid LOG_LEVEL",
			env:     map[string]string{"DATABASE_URL": "postgres://db", "LOG_LEVEL": "loud"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := load(func(k string) string { return tt.env[k] })
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got config %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
