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
	if cfg.S3.AccessKey != "" {
		t.Fatal("s3 access key must not have default")
	}
	if cfg.S3.SecretKey != "" {
		t.Fatal("s3 secret key must not have default")
	}
	if cfg.Broker.URI != "" {
		t.Fatal("rabbitmq uri must not have default")
	}
}

func TestValidateFlags(t *testing.T) {
	t.Run("missing sensitive config", func(t *testing.T) {
		err := validateFlags(&Flags{})
		if err == nil {
			t.Fatal("expected error")
		}
		want := "required configuration is not set: DATABASE_URI, S3_ACCESS_KEY, S3_SECRET_KEY, RABBITMQ_URI"
		if err.Error() != want {
			t.Fatalf("got %q, want %q", err.Error(), want)
		}
	})

	t.Run("all required config set", func(t *testing.T) {
		err := validateFlags(&Flags{
			DatabaseURI: "postgres://x",
			S3AccessKey: "key",
			S3SecretKey: "secret",
			RabbitURI:   "amqp://user:pass@localhost:5672/",
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestParseFlagsRequiresSensitiveConfig(t *testing.T) {
	conf := &Config{
		Server: ServerConfig{Address: ":8080", PublicURL: "http://localhost:8080"},
		S3:     S3Config{Endpoint: "localhost:9000", Bucket: "avatars"},
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags, err := parseFlags(conf, fs, []string{
		"-d", "postgres://y",
		"-r", "amqp://user:pass@localhost:5672/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFlags(flags); err == nil {
		t.Fatal("expected error for missing s3 credentials")
	}
}
