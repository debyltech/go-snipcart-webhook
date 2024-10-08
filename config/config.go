package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/EasyPost/easypost-go/v4"
	"github.com/aws/aws-secretsmanager-caching-go/secretcache"
	"github.com/caarlos0/env/v7"
	"github.com/joho/godotenv"
)

type Config struct {
	EIN string `env:"GSW_EIN,unset"`

	SnipcartApiKey string `env:"SNIPCART_API_KEY,unset"`
	EasypostApiKey string `env:"EASYPOST_API_KEY,unset"`

	AwsSmsArn string `env:"GSW_SMS_SECRET_ARN,unset"`

	Production bool `env:"GSW_PRODUCTION" envDefault:"false"`

	ManufactureCountry string `env:"GSW_MFGR_COUNTRY" envDefault:"US"`
	SenderAddressJson  string `env:"GSW_SENDER_JSON,required"`
	SenderAddress      *easypost.Address

	DefaultParcelJson string `env:"GSW_PARCEL_JSON,required"`
	DefaultParcel     *easypost.Parcel

	AllowedCarriers string `env:"GSW_ALLOWED_CARRIERS" envDefault:"USPS"`

	ShippingDiscount int `env:"GSW_SHIP_DISCOUNT" envDefault:"0"`

	VAT             string `env:"GSW_VAT,unset"`
	IOSS            string `env:"GSW_IOSS,unset"`
	CustomsVerifier string `env:"GSW_CUSTOMSVERIFIER,unset"`
}

type WebhookSmsSecret struct {
	SnipcartApiKey string `json:"snipcart_api_key"`
	EasypostApiKey string `json:"easypost_api_key"`
}

func (c *Config) CarrierAllowed(carrier string) bool {
	return slices.Contains(strings.Split(c.AllowedCarriers, ","), carrier)
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

	var senderAddress easypost.Address
	if err := json.Unmarshal([]byte(config.SenderAddressJson), &senderAddress); err != nil {
		return &config, err
	}
	config.SenderAddress = &senderAddress

	var defaultParcel easypost.Parcel
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
			return &config, fmt.Errorf("issue with GetSecretString: %s", err.Error())
		}

		err = json.Unmarshal([]byte(secretString), &webhookSmsSecret)
		if err != nil {
			return &config, fmt.Errorf("issue with unmarshal: %s", err.Error())
		}

		config.SnipcartApiKey = webhookSmsSecret.SnipcartApiKey
		config.EasypostApiKey = webhookSmsSecret.EasypostApiKey
	}

	return &config, nil
}
