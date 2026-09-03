// Package config загружает конфигурацию сервиса из переменных окружения и флагов.
package config

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/caarlos0/env/v11"
)

// Config — конфигурация из окружения.
type Config struct {
	Server ServerConfig
	DB     DBConfig
	S3     S3Config
	Broker BrokerConfig
}

// ServerConfig описывает сетевые параметры сервера.
type ServerConfig struct {
	Address   string `env:"RUN_ADDRESS" envDefault:":8080"`
	PublicURL string `env:"PUBLIC_URL" envDefault:"http://localhost:8080"`
}

// DBConfig описывает подключение к PostgreSQL.
type DBConfig struct {
	DatabaseURI string `env:"DATABASE_URI"`
}

// S3Config описывает подключение к S3-совместимому хранилищу.
type S3Config struct {
	Endpoint  string `env:"S3_ENDPOINT" envDefault:"localhost:9000"`
	AccessKey string `env:"S3_ACCESS_KEY"`
	SecretKey string `env:"S3_SECRET_KEY"`
	Bucket    string `env:"S3_BUCKET" envDefault:"avatars"`
	UseSSL    bool   `env:"S3_USE_SSL" envDefault:"false"`
}

// BrokerConfig описывает подключение к RabbitMQ.
type BrokerConfig struct {
	URI string `env:"RABBITMQ_URI"`
}

// Flags — итоговые параметры запуска сервера и воркера.
type Flags struct {
	RunAddress  string
	PublicURL   string
	DatabaseURI string
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    bool
	RabbitURI   string
}

// loadConfig читает конфигурацию из переменных окружения.
func loadConfig() (*Config, error) {
	cfg := Config{}
	if err := env.Parse(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// parseFlags накладывает CLI-флаги поверх значений из окружения.
func parseFlags(conf *Config, fs *flag.FlagSet, args []string) (*Flags, error) {
	flags := &Flags{
		RunAddress:  conf.Server.Address,
		PublicURL:   conf.Server.PublicURL,
		DatabaseURI: conf.DB.DatabaseURI,
		S3Endpoint:  conf.S3.Endpoint,
		S3AccessKey: conf.S3.AccessKey,
		S3SecretKey: conf.S3.SecretKey,
		S3Bucket:    conf.S3.Bucket,
		S3UseSSL:    conf.S3.UseSSL,
		RabbitURI:   conf.Broker.URI,
	}

	fs.StringVar(&flags.RunAddress, "a", flags.RunAddress, "service run address")
	fs.StringVar(&flags.PublicURL, "u", flags.PublicURL, "public base url")
	fs.StringVar(&flags.DatabaseURI, "d", flags.DatabaseURI, "postgres connection uri")
	fs.StringVar(&flags.S3Endpoint, "s3-endpoint", flags.S3Endpoint, "s3 endpoint host:port")
	fs.StringVar(&flags.S3Bucket, "s3-bucket", flags.S3Bucket, "s3 bucket name")
	fs.StringVar(&flags.RabbitURI, "r", flags.RabbitURI, "rabbitmq uri")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return flags, nil
}

func validateFlags(flags *Flags) error {
	var missing []string
	if flags.DatabaseURI == "" {
		missing = append(missing, "DATABASE_URI")
	}
	if flags.S3AccessKey == "" {
		missing = append(missing, "S3_ACCESS_KEY")
	}
	if flags.S3SecretKey == "" {
		missing = append(missing, "S3_SECRET_KEY")
	}
	if flags.RabbitURI == "" {
		missing = append(missing, "RABBITMQ_URI")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("required configuration is not set: %s", strings.Join(missing, ", "))
}

// LoadFlags загружает конфигурацию из окружения и CLI-флагов.
func LoadFlags() (*Flags, error) {
	conf, err := loadConfig()
	if err != nil {
		return nil, err
	}
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	flags, err := parseFlags(conf, fs, os.Args[1:])
	if err != nil {
		return nil, err
	}
	if err := validateFlags(flags); err != nil {
		return nil, err
	}
	return flags, nil
}
