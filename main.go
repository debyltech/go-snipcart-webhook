package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/debyltech/go-shippr/shippo"
	"github.com/debyltech/go-snipcart-webhook/config"
	"github.com/debyltech/go-snipcart/snipcart"
	"github.com/gin-gonic/gin"
)

const (
	ValidateUrl string = "https://app.snipcart.com/api/requestvalidation/"
)

type ShippingWebhookEvent struct {
	EventName string                 `json:"eventName"`
	CreatedOn time.Time              `json:"createdOn"`
	Order     snipcart.SnipcartOrder `json:"content"`
}

type ShippingRatesResponse struct {
	Cost         float64 `json:"cost"`
	Description  string  `json:"description"`
	DeliveryDays int     `json:"guaranteedDaysToDelivery"`
}

func ValidateWebhook(token string, snipcartApiKey string) error {
	validateRequest, err := http.NewRequest("GET", ValidateUrl+token, nil)
	if err != nil {
		return err
	}

	client := &http.Client{}

	auth := base64.StdEncoding.EncodeToString([]byte(snipcartApiKey + ":"))
	validateRequest.Header.Set("Authorization", fmt.Sprintf("Basic %s", auth))

	validateResponse, err := client.Do(validateRequest)
	if err != nil {
		return err
	}

	if validateResponse.StatusCode < 200 || validateResponse.StatusCode >= 300 {
		return fmt.Errorf("non-2XX status code: %d", validateResponse.StatusCode)
	}

	return nil
}

func HandleShippingRates(config *config.Config, shippoClient *shippo.Client) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		validationHeader := c.GetHeader("X-Snipcart-RequestToken")
		if validationHeader == "" {
			c.AbortWithError(http.StatusBadRequest, errors.New("missing X-Snipcart-RequestToken header"))
			return
		}
		err := ValidateWebhook(validationHeader, config.SnipcartApiKey)
		if err != nil {
			c.AbortWithError(http.StatusBadRequest, err)
			return
		}

		var event ShippingWebhookEvent
		err = json.NewDecoder(c.Request.Body).Decode(&event)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}

		defer c.Request.Body.Close()

		var lineItems []shippo.LineItem
		for _, v := range event.Order.Items {
			lineItems = append(lineItems, shippo.LineItem{
				Quantity:           v.Quantity,
				TotalPrice:         fmt.Sprintf("%.2f", v.TotalPrice),
				Currency:           event.Order.Currency,
				Weight:             fmt.Sprintf("%.2f", v.Weight),
				WeightUnit:         config.WeightUnit,
				Title:              v.Name,
				ManufactureCountry: config.ManufactureCountry,
				Sku:                v.ID,
			})
		}

		parcel := config.DefaultParcel
		parcel.WeightUnit = config.WeightUnit
		parcel.DistanceUnit = config.DimensionUnit
		parcel.Weight = fmt.Sprintf("%.2f", event.Order.TotalWeight)

		rateRequest := shippo.RateRequest{
			AddressFrom: shippo.Address{
				Name:       config.SenderAddress.Name,
				Address1:   config.SenderAddress.Address1,
				Address2:   config.SenderAddress.Address2,
				City:       config.SenderAddress.City,
				State:      config.SenderAddress.State,
				Country:    config.SenderAddress.Country,
				PostalCode: config.SenderAddress.PostalCode,
			},
			AddressTo: shippo.Address{
				Name:       event.Order.Name,
				Company:    event.Order.Company,
				Address1:   event.Order.Address1,
				Address2:   event.Order.Address2,
				City:       event.Order.City,
				Country:    event.Order.Country,
				State:      event.Order.Province,
				PostalCode: event.Order.PostalCode,
				Phone:      event.Order.Phone,
				Email:      event.Order.Email,
			},
			LineItems: lineItems,
			Parcel:    parcel,
		}

		rateResponse, err := shippoClient.GenerateRates(rateRequest)
		if err != nil {
			c.AbortWithError(http.StatusBadRequest, err)
			return
		}

		var rates []ShippingRatesResponse
		for _, v := range rateResponse.Rates {
			cost, err := strconv.ParseFloat(v.Amount, 64)
			if err != nil {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}
			rates = append(rates, ShippingRatesResponse{
				Cost:         cost,
				Description:  v.Title,
				DeliveryDays: v.EstimatedDays,
			})
		}

		c.JSON(http.StatusOK, rates)
	}

	return fn
}

func main() {
	bindPort := flag.String("bind", "8080", "port to bind to")
	releaseMode := flag.Bool("release", false, "true if setting gin to release mode")
	configPath := flag.String("config", "", "path to config.json")
	flag.Parse()

	if *configPath == "" {
		log.Fatal("config path not defined")
	}

	config, err := config.NewConfigFromFile(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	shippoClient := shippo.NewClient(config.ShippoApiKey)

	if *releaseMode {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	api := r.Group("/api")
	{
		v1 := api.Group("/v1")
		{
			v1.POST("/shipping", HandleShippingRates(config, &shippoClient))
		}
	}

	r.Run(fmt.Sprintf(":%s", *bindPort))
}
