package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/debyltech/go-shippr/shippo"
	"github.com/debyltech/go-snipcart-webhook/config"
	"github.com/debyltech/go-snipcart/snipcart"
	"github.com/gin-gonic/gin"
)

const (
	ValidateUrl string = "https://app.snipcart.com/api/requestvalidation/"
)

type WebhookEvent struct {
	EventName string `json:"eventName"`
}

type ShippingWebhookEvent struct {
	EventName string                 `json:"eventName"`
	CreatedOn time.Time              `json:"createdOn"`
	Order     snipcart.SnipcartOrder `json:"content"`
}

type OrderCompleteWebhookEvent struct {
	EventName string                             `json:"eventName"`
	CreatedOn time.Time                          `json:"createdOn"`
	Content   snipcart.SnipcartOrderEventContent `json:"content"`
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
		return fmt.Errorf("error validating webhook: %s", err.Error())
	}

	if validateResponse.StatusCode < 200 || validateResponse.StatusCode >= 300 {
		return fmt.Errorf("non-2XX status code for validating webhook: %d", validateResponse.StatusCode)
	}

	return nil
}

func HandleShippingRates(body io.ReadCloser, config *config.Config, shippoClient *shippo.Client) (any, error) {
	var event ShippingWebhookEvent
	if err := json.NewDecoder(body).Decode(&event); err != nil {
		return nil, fmt.Errorf("error with shipping event decode: %s", err.Error())
	}

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

	rateResponse, err := shippoClient.GenerateRates(rateRequest)
	if err != nil {
		return nil, fmt.Errorf("error with generating rates: %s; request: %v", err.Error(), rateRequest)
	}

	var shippingRates ShippingRatesResponse
	for _, v := range rateResponse.Rates {
		cost, err := strconv.ParseFloat(v.Amount, 64)
		if err != nil {
			return nil, fmt.Errorf("error with parsing float in shipping rates: %s", err.Error())
		}
		shippingRates.Rates = append(shippingRates.Rates, ShippingRate{
			Cost:        cost,
			Description: fmt.Sprintf("%s - Estimated arrival in %d days", v.Title, v.EstimatedDays),
		})
	}

	return shippingRates, nil
}

func HandleOrderComplete(body io.ReadCloser, config *config.Config, shippoClient *shippo.Client) (int, error) {
	var event OrderCompleteWebhookEvent
	if err := json.NewDecoder(body).Decode(&event); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with ordercomplete event decode: %s", err.Error())
	}

	var lineItems []shippo.LineItem
	for _, v := range event.Content.Items {
		lineItems = append(lineItems, shippo.LineItem{
			Quantity:           v.Quantity,
			TotalPrice:         fmt.Sprintf("%.2f", v.TotalPrice),
			Currency:           strings.ToUpper(event.Content.Currency),
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
	parcel.Weight = fmt.Sprintf("%.2f", event.Content.TotalWeight)

	shipmentRequest := shippo.Shipment{
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
			Name:       event.Content.ShippingAddress.Name,
			Company:    event.Content.ShippingAddress.Company,
			Address1:   event.Content.ShippingAddress.Address1,
			Address2:   event.Content.ShippingAddress.Address2,
			City:       event.Content.ShippingAddress.City,
			Country:    event.Content.ShippingAddress.Country,
			State:      event.Content.ShippingAddress.Province,
			PostalCode: event.Content.ShippingAddress.PostalCode,
			Phone:      event.Content.ShippingAddress.Phone,
			Email:      event.Content.Email,
		},
		Parcels: []shippo.Parcel{parcel},
	}

	_, err := shippoClient.CreateShipment(shipmentRequest)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with creating shipment: %s", err.Error())
	}

	// CREATE SHIPPO order
	return http.StatusOK, nil
}

func RouteSnipcartWebhook(config *config.Config, shippoClient *shippo.Client) gin.HandlerFunc {
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

		rawBody, err := ioutil.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
		}
		var event WebhookEvent
		if err := json.NewDecoder(ioutil.NopCloser(bytes.NewBuffer(rawBody))).Decode(&event); err != nil {
			c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("error decoding generic webhook event: %s", err.Error()))
			return
		}

		switch event.EventName {
		case "order.completed":
			statusCode, err := HandleOrderComplete(ioutil.NopCloser(bytes.NewBuffer(rawBody)), config, shippoClient)
			if err != nil {
				c.AbortWithError(statusCode, err)
				return
			}

			c.Data(statusCode, gin.MIMEHTML, nil)
		case "shippingrates.fetch":
			response, err := HandleShippingRates(ioutil.NopCloser(bytes.NewBuffer(rawBody)), config, shippoClient)
			if err != nil {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}

			c.JSON(http.StatusOK, response)
		}
	}

	return fn
}

func main() {
	bindPort := flag.String("bind", "8080", "port to bind to")
	releaseMode := flag.Bool("release", false, "true if setting gin to release mode")
	configPath := flag.String("config", "", "path to config.json")
	logPath := flag.String("logfile", "/var/log/go-snipcart/access.log", "path to logfile")

	flag.Parse()

	logDir, logFile := filepath.Split(*logPath)
	err := os.MkdirAll(logDir, os.ModePerm)
	if err != nil {
		log.Fatal(err)
	}

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

	f, err := os.Create(filepath.Join(logDir, logFile))
	if err != nil {
		log.Fatal(err)
	}

	gin.DefaultWriter = io.MultiWriter(os.Stdout, f)
	r := gin.Default()

	webhooks := r.Group("/webhooks")
	{
		webhooks.POST("/snipcart", RouteSnipcartWebhook(config, &shippoClient))
	}

	if err := r.Run(fmt.Sprintf(":%s", *bindPort)); err != nil {
		log.Fatal(err)
	}
}
