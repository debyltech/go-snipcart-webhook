package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/debyltech/go-shippr/shippo"
	"github.com/debyltech/go-snipcart-webhook/config"
	"github.com/debyltech/go-snipcart/snipcart"
	"github.com/gin-contrib/logger"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	ValidateUrl string = "https://app.snipcart.com/api/requestvalidation/"
)

var (
	ginlogger zerolog.Logger
)

type ShippingWebhookEvent struct {
	EventName string                 `json:"eventName"`
	CreatedOn time.Time              `json:"createdOn"`
	Order     snipcart.SnipcartOrder `json:"content"`
}

type ShippingRate struct {
	Cost        float64 `json:"cost"`
	Description string  `json:"description"`
	// DeliveryDays int     `json:"guaranteedDaysToDelivery"`
}

type ShippingRatesResponse struct {
	Rates []ShippingRate `json:"rates"`
}

func ValidateWebhook(token string, snipcartApiKey string) error {
	validateRequest, err := http.NewRequest("GET", ValidateUrl+token, nil)
	if err != nil {
		return err
	}

	client := &http.Client{}

	auth := base64.StdEncoding.EncodeToString([]byte(snipcartApiKey + ":"))
	validateRequest.Header.Set("Authorization", fmt.Sprintf("Basic %s", auth))
	validateRequest.Header.Set("Accept", "application/json")

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
		if err := ValidateWebhook(validationHeader, config.SnipcartApiKey); err != nil {
			c.AbortWithError(http.StatusBadRequest, err)
			return
		}

		var event ShippingWebhookEvent
		if err := json.NewDecoder(c.Request.Body).Decode(&event); err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}

		defer c.Request.Body.Close()

		var lineItems []shippo.LineItem
		for _, v := range event.Order.Items {
			lineItems = append(lineItems, shippo.LineItem{
				Quantity:           v.Quantity,
				TotalPrice:         fmt.Sprintf("%.2f", v.TotalPrice),
				Currency:           strings.ToUpper(event.Order.Currency),
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

		ginlogger.Info().Interface("rate_request", rateRequest)

		rateResponse, err := shippoClient.GenerateRates(rateRequest)
		if err != nil {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("err: %v, request: %v", err, rateRequest))
			return
		}

		var shippingRates ShippingRatesResponse
		for _, v := range rateResponse.Rates {
			cost, err := strconv.ParseFloat(v.Amount, 64)
			if err != nil {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}
			shippingRates.Rates = append(shippingRates.Rates, ShippingRate{
				Cost:        cost,
				Description: fmt.Sprintf("%s - Estimated arrival in %d days", v.Title, v.EstimatedDays),
			})
		}
		ginlogger.Info().Interface("shipping_rates", shippingRates)

		c.JSON(http.StatusOK, shippingRates)
	}

	return fn
}

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	bindPort := flag.String("bind", "8080", "port to bind to")
	releaseMode := flag.Bool("release", false, "true if setting gin to release mode")
	configPath := flag.String("config", "", "path to config.json")
	flag.Parse()

	if *configPath == "" {
		log.Fatal().Msg("config path not defined")
	}

	config, err := config.NewConfigFromFile(*configPath)
	if err != nil {
		log.Fatal().Err(err)
	}

	shippoClient := shippo.NewClient(config.ShippoApiKey)

	if *releaseMode {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	ginlogger = zerolog.New(gin.DefaultWriter).With().Timestamp().Logger()
	r.Use(gin.Recovery(), logger.SetLogger(logger.WithLogger(func(_ *gin.Context, l zerolog.Logger) zerolog.Logger {
		return l.Output(gin.DefaultWriter).With().Logger()
	})))

	api := r.Group("/api")
	{
		v1 := api.Group("/v1")
		{
			v1.POST("/shipping", HandleShippingRates(config, &shippoClient))
		}
	}

	if err := r.Run(fmt.Sprintf(":%s", *bindPort)); err != nil {
		log.Fatal().Err(err)
	}
}
