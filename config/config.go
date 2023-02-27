package config

import (
	"bytes"
	"encoding/json"
	"io/ioutil"

	"github.com/debyltech/go-shippr/shippo"
)

type Config struct {
	SnipcartApiKey     string         `json:"snipcart_api_key"`
	ShippoApiKey       string         `json:"shippo_api_key"`
	WeightUnit         string         `json:"weight_unit"`
	DimensionUnit      string         `json:"dimension_unit"`
	ManufactureCountry string         `json:"manufacture_country"`
	SenderAddress      shippo.Address `json:"sender_address"`
	DefaultParcel      shippo.Parcel  `json:"default_parcel"`
}

func NewConfigFromFile(filePath string) (*Config, error) {
	configBytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var config Config
	err = json.NewDecoder(bytes.NewBuffer(configBytes)).Decode(&config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}
