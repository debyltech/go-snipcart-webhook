package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-secretsmanager-caching-go/secretcache"
	"github.com/caarlos0/env/v7"
	"github.com/debyltech/go-shippr/shippo"
	"github.com/joho/godotenv"
)

type Config struct {
	SnipcartApiKey        string `env:"SNIPCART_API_KEY,unset"`
	ShippoApiKey          string `env:"SHIPPO_API_KEY,unset"`
	WeightUnit            string `env:"GSW_WEIGHT_UNIT" envDefault:"g"`
	DimensionUnit         string `env:"GSW_DIM_UNIT" envDefault:"cm"`
	ManufactureCountry    string `env:"GSW_MFGR_COUNTRY" envDefault:"US"`
	SenderAddressJson     string `env:"GSW_SENDER_JSON,required"`
	SenderAddress         *shippo.Address
	DefaultParcelJson     string `env:"GSW_PARCEL_JSON,required"`
	DefaultParcel         *shippo.Parcel
	Production            bool   `env:"GSW_PRODUCTION" envDefault:"false"`
	AwsSmsArn             string `env:"GSW_SMS_SECRET_ARN,unset"`
	RateServiceLevels     string `env:"GSW_SERVICELEVELS" envDefault:"usps_first"`
	RateServiceLevelsList []string
}

type WebhookSmsSecret struct {
	SnipcartApiKey string `json:"snipcart_api_key"`
	ShippoApiKey   string `json:"shippo_api_key"`
}

func (c *Config) ServiceLevelAllowed(serviceLevel string) bool {
	for _, v := range c.RateServiceLevelsList {
		if serviceLevel == v {
			return true
		}
	}

	return false
}

func NewConfigFromFile(filePath string) (*Config, error) {
	if err := godotenv.Load(filePath); err != nil {
		return &Config{}, err
	}

	// loading from a file so we assume not to use Sms, maybe change this in the
	// future?
	config, err := NewConfigFromEnv(false)
	if err != nil {
		return config, err
	}

	return config, nil
}

func NewConfigFromEnv(useAwsSms bool) (*Config, error) {
	var config Config
	if err := env.Parse(&config); err != nil {
		return &Config{}, err
	}

	config.RateServiceLevelsList = strings.Split(config.RateServiceLevels, ",")

	var senderAddress shippo.Address
	if err := json.Unmarshal([]byte(config.SenderAddressJson), &senderAddress); err != nil {
		return &config, err
	}
	config.SenderAddress = &senderAddress

	var defaultParcel shippo.Parcel
	if err := json.Unmarshal([]byte(config.DefaultParcelJson), &defaultParcel); err != nil {
		return &config, err
	}
	config.DefaultParcel = &defaultParcel

	if useAwsSms {
		secretCache, err := secretcache.New()
		if err != nil {
			return &config, nil
		}

		var webhookSmsSecret WebhookSmsSecret
		secretString, err := secretCache.GetSecretString(config.AwsSmsArn)
		if err != nil {
			return &config, fmt.Errorf("issue with GetSecretString: %s\n", err.Error())
		}

		err = json.Unmarshal([]byte(secretString), &webhookSmsSecret)
		if err != nil {
			return &config, fmt.Errorf("issue with unmarshal: %s\n", err.Error())
		}

		config.SnipcartApiKey = webhookSmsSecret.SnipcartApiKey
		config.ShippoApiKey = webhookSmsSecret.ShippoApiKey
	}

	return &config, nil
}
