package pg

import "github.com/light-speak/luxo/pkg/lux"

// ConfigFromEnv reads database configuration from environment variables.
func ConfigFromEnv() lux.DBConfig {
	return lux.DBConfigFromEnv()
}
