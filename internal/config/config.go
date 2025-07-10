package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	BrickLink BrickLinkConfig `mapstructure:"bricklink"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Redis     RedisConfig     `mapstructure:"redis"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Port int    `mapstructure:"port"`
	Host string `mapstructure:"host"`
}

// BrickLinkConfig holds BrickLink API configuration
type BrickLinkConfig struct {
	BaseURL              string   `mapstructure:"base_url"`
	Timeout              int      `mapstructure:"timeout"`
	MaxRetries           int      `mapstructure:"max_retries"`
	MaxWorkers           int      `mapstructure:"max_workers"`
	MaxRequestsPerSecond int      `mapstructure:"max_requests_per_second"`
	Proxies              []string `mapstructure:"proxies"`

	// Authentication
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Name     string `mapstructure:"name"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
}

// RedisConfig holds Redis connection details
type RedisConfig struct {
	Host          string `mapstructure:"host"`
	Port          int    `mapstructure:"port"`
	Password      string `mapstructure:"password"`
	Database      int    `mapstructure:"database"`
	ConsumerGroup string `mapstructure:"consumer_group"`
	MinIdleTime   int    `mapstructure:"min_idle_time"`
}

// Load loads configuration from YAML file with environment variable overrides
func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	setDefaults()

	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return nil, fmt.Errorf("config.yaml file not found in current directory")
		}
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	if err := viper.MergeInConfig(); err != nil {
		return nil, fmt.Errorf("error merging config file: %w", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("unable to decode config: %w", err)
	}

	return &config, nil
}

func setDefaults() {
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.host", "localhost")

	viper.SetDefault("bricklink.base_url", "http://www.bricklink.com")
	viper.SetDefault("bricklink.timeout", 30)
	viper.SetDefault("bricklink.max_retries", 3)
	viper.SetDefault("bricklink.max_workers", 10)
	viper.SetDefault("bricklink.username", "")
	viper.SetDefault("bricklink.password", "")
	viper.SetDefault("bricklink.cookie_file", "./cookies.json")
	viper.SetDefault("bricklink.login_on_start", true)

	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.name", "bricklink")
	viper.SetDefault("database.user", "bricklink_user")
	viper.SetDefault("database.password", "bricklink_pass")

	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.password", "redis_pass")
	viper.SetDefault("redis.database", 0)
	viper.SetDefault("redis.consumer_group", "bricklink_consumer")
	viper.SetDefault("redis.min_idle_time", 120)
}
