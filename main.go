package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/debyltech/go-shippr/shippo"
	"github.com/debyltech/go-snipcart/snipcart"
	"github.com/debyltech/go-snipcart/snipcart/webhook"
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

func ValidateWebhook(token string) error {
	validateRequest, err := http.Get(ValidateUrl + token)
	if err != nil {
		return err
	}

	if validateRequest.StatusCode < 200 || validateRequest.StatusCode >= 300 {
		return errors.New("non-2XX response received")
	}

	return nil
}

func HandleShippingRates(config *webhook.Config, shippoClient *shippo.Client) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		validationHeader := c.GetHeader("X-Snipcart-RequestToken")
		if validationHeader == "" {
			c.AbortWithError(http.StatusBadRequest, errors.New("missing X-Snipcart-RequestToken header"))
			return
		}
		err := ValidateWebhook(validationHeader)
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
	configPath := flag.String("config", "", "path to config.json")
	flag.Parse()

	if *configPath == "" {
		log.Fatal("config path not defined")
	}

	config, err := webhook.NewConfigFromFile(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	shippoClient := shippo.NewClient(config.ShippoApiKey)

	r := gin.Default()

	api := r.Group("/api")
	{
		v1 := api.Group("/v1")
		{
			v1.POST("/shipping", HandleShippingRates(config, &shippoClient))
		}
	}

	r.Run("localhost:8081")
}
