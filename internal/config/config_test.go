package config

import (
	"flag"
	"testing"
)

func TestParseFlags(t *testing.T) {
	conf := &Config{
		Server: ServerConfig{Address: ":8080", PublicURL: "http://localhost:8080"},
		DB:     DBConfig{DatabaseURI: "postgres://x"},
		S3:     S3Config{Endpoint: "localhost:9000", Bucket: "avatars"},
		Broker: BrokerConfig{URI: "amqp://guest:guest@localhost:5672/"},
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags, err := parseFlags(conf, fs, []string{"-a", ":9090", "-d", "postgres://y"})
	if err != nil {
		t.Fatal(err)
	}
	if flags.RunAddress != ":9090" {
		t.Fatal(flags.RunAddress)
	}
	if flags.DatabaseURI != "postgres://y" {
		t.Fatal(flags.DatabaseURI)
	}
}

func TestLoadConfig(t *testing.T) {
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Address == "" {
		t.Fatal("empty address")
	}
	if cfg.S3.Bucket == "" {
		t.Fatal("empty bucket")
	}
}
