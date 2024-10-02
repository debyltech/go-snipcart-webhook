package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EasyPost/easypost-go/v4"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/debyltech/go-shippr/shippo"
	"github.com/debyltech/go-snipcart-webhook/config"
	"github.com/debyltech/go-snipcart/snipcart"
	"github.com/gin-gonic/gin"
)

type WebhookEvent struct {
	EventName string `json:"eventName"`
}

type ShippingRateFetchWebhookEvent struct {
	EventName string         `json:"eventName"`
	CreatedOn time.Time      `json:"createdOn"`
	Order     snipcart.Order `json:"content"`
}

type OrderCompleteWebhookEvent struct {
	EventName string         `json:"eventName"`
	CreatedOn time.Time      `json:"createdOn"`
	Order     snipcart.Order `json:"content"`
}

type ShippingRate struct {
	Id          string  `json:"userDefinedId"`
	Cost        float64 `json:"cost"`
	Description string  `json:"description"`
	// We cannot guarantee days to delivery, kept here for debug future
	// DeliveryDays int     `json:"guaranteedDaysToDelivery"`
}

func HandleShippingRates(body io.ReadCloser, easypostClient *easypost.Client) (any, error) {
	var err error
	var event ShippingRateFetchWebhookEvent

	// Decode the incoming json and check validity
	if err := json.NewDecoder(body).Decode(&event); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with shipping rate fetch event decode: %s", err.Error())
	}

	logJson("shippingrates.fetch", event.Order.Token)

	// Validate Address Fields
	if err := ValidateAddressFields(event.Order.ShippingAddress, webhookConfig.Production); err != nil {
		return err, nil
	}

	// Get customs items if needed
	if IsInternational(event.Order.ShippingAddress.Country) {
		// TODO: move this to a func
		customsItems := GenerateCustomsItems(event.Order)
		var customsDeclaration *shippo.CustomsDeclaration

		customsInfo := easypost.CustomsInfo{
			CustomsCertify:    true,
			CustomsSigner:     webhookConfig.CustomsVerifier,
			RestrictionType:   RSTRCTTYP_NONE,
			EELPFC:            EEL_NOEEI3037a,
			CustomsItems:      customsItems,
			NonDeliveryOption: NONDELIV_RETURN, // TODO: do we ever want to abandon?
			ContentsType:      CONTYP_MERCH,
		}

		/* Handle special Canadial EEL/PFC */
		if strings.ToLower(event.Order.Country) == "ca" {
			customsInfo.EELPFC = EEL_NOEEI3036
		}

		/* Handle EU Countries */
		if IsEUCountry(event.Order.Country) {
			declaration.ExporterIdentification = shippo.ExporterIdentification{
				TaxId: shippo.CustomsTaxId{
					Number: webhookConfig.IOSS,
					Type:   shippo.TAX_IOSS,
				},
			}

			declaration.VatCollected = true
		}

		var err error
		customsDeclaration, err = shippoClient.CreateCustomsDeclaration(declaration)
		if err != nil {
			return http.StatusInternalServerError, fmt.Errorf("error with creating customs declaration: %s", err.Error())
		}

	}

	parcel := webhookConfig.Parcel
	parcel.WeightUnit = webhookConfig.WeightUnit
	parcel.DistanceUnit = webhookConfig.DimensionUnit
	parcel.Weight = fmt.Sprintf("%.2f", event.Order.TotalWeight)

	shipmentRequest := shippo.Shipment{
		AddressFrom: shippo.Address{
			Name:       webhookConfig.SenderAddress.Name,
			Address1:   webhookConfig.SenderAddress.Address1,
			Address2:   webhookConfig.SenderAddress.Address2,
			City:       webhookConfig.SenderAddress.City,
			State:      webhookConfig.SenderAddress.State,
			Country:    webhookConfig.SenderAddress.Country,
			PostalCode: webhookConfig.SenderAddress.PostalCode,
			Phone:      webhookConfig.SenderAddress.Phone,
		},
		AddressTo: shippo.Address{
			Name:       event.Order.ShippingAddress.Name,
			Company:    event.Order.ShippingAddress.Company,
			Address1:   event.Order.ShippingAddress.Address1,
			Address2:   event.Order.ShippingAddress.Address2,
			City:       event.Order.ShippingAddress.City,
			Country:    event.Order.ShippingAddress.Country,
			State:      event.Order.ShippingAddress.Province,
			PostalCode: event.Order.ShippingAddress.PostalCode,
			Phone:      event.Order.ShippingAddress.Phone,
			Email:      event.Order.Email,
		},
		Parcels: []shippo.Parcel{*parcel},
	}

	if customsDeclaration != nil {
		shipmentRequest.CustomsDeclaration = customsDeclaration
	}

	var shipmentResponse *shippo.Shipment
	// Check if we already have a shipment
	if event.Order.ShippingRateId != "" {
		shipmentId := strings.Split(event.Order.ShippingRateId, ";")[0]
		shipmentResponse, err = shippoClient.GetShipmentById(shipmentId)
		if err != nil {
			return http.StatusInternalServerError, fmt.Errorf("error with fetching existing shipment: %s", err.Error())
		}
	} else {
		DebugPrintf("creating shipment")
		shipmentResponse, err = shippoClient.CreateShipment(shipmentRequest)
		if err != nil {
			return http.StatusInternalServerError, fmt.Errorf("error with creating shipment: %s", err.Error())
		}
	}

	if len(shipmentResponse.Messages) > 0 {
		DebugPrintf("WARNING Shipment messages: %v", shipmentResponse.Messages)
	}

	DebugPrintf("awaiting shipment creation succeessful...")
	err = shippoClient.AwaitQueuedFinished(shipmentResponse.Id)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with awaiting shipment status: %s", err.Error())
	}

	ratesResponse, err := shippoClient.GetRatesForShipmentId(shipmentResponse.Id)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with getting rates: %s", err.Error())
	}
	DebugPrintf("got successful shipping rate response from shippo")

	DebugPrintf("creating shipping rates for snipcart...")
	var shippingRates ShippingRatesResponse
	for _, rate := range ratesResponse.Rates {
		if !webhookConfig.ServiceLevelAllowed(rate.ServiceLevel.Token) {
			logJson("shippingates.fetch", fmt.Sprintf("skipped shipping service level '%s'", rate.ServiceLevel.Token))
			continue
		}

		cost, err := strconv.ParseFloat(rate.Amount, 64)
		if err != nil {
			return nil, fmt.Errorf("error with parsing float in shipping rates: %s", err.Error())
		}

		logJson("shippingrates.fetch", fmt.Sprintf("adding shipping service level '%s' costing '%.2f' with id '%s;%s'", rate.ServiceLevel.Name, cost, shipmentResponse.Id, rate.Id))
		shippingRates.Rates = append(shippingRates.Rates, ShippingRate{
			Id:          fmt.Sprintf("%s;%s", shipmentResponse.Id, rate.Id),
			Cost:        cost,
			Description: fmt.Sprintf("%s %s - Estimated arrival in %d days", rate.Provider, rate.ServiceLevel.Name, rate.EstimatedDays),
		})
	}

	logJson("shippingrates.fetch", fmt.Sprintf("completed for %s", event.Order.Token))

	return shippingRates, nil
}

func HandleOrderComplete(body io.ReadCloser, shippoClient *shippo.Client) (int, error) {
	var event OrderCompleteWebhookEvent
	if err := json.NewDecoder(body).Decode(&event); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with ordercomplete event decode: %s", err.Error())
	}

	logJson("order.completed", event.Order.Token)

	if !webhookConfig.Production {
		jsonEvent, _ := json.Marshal(event)
		DebugPrintln(string(jsonEvent))
	}

	return http.StatusOK, nil
}

func HandleTaxCalculation(body io.ReadCloser) (*snipcart.TaxResponse, error) {
	var taxes snipcart.TaxResponse

	var event snipcart.TaxWebhook
	if err := json.NewDecoder(body).Decode(&event); err != nil {
		return &taxes, fmt.Errorf("error with taxescalculate event decode: %s", err.Error())
	}

	logJson("taxes.calculate", event.Content.Token)

	var taxAddress *snipcart.Address = &event.Content.ShippingAddress

	if event.Content.ShipToBillingAddress {
		taxAddress = &event.Content.BillingAddress
	}

	DebugPrintf("successfully decoded webhook tax POST content -- state %s country %s", taxAddress.Province, taxAddress.Country)

	/* Tax - EU */
	if IsEUCountry(taxAddress.Country) {
		DebugPrintf("detected EU country for Tax calculation: %s", taxAddress.Country)

		taxes.Taxes = append(taxes.Taxes, snipcart.Tax{
			Name:             "VAT",
			Amount:           event.Content.ItemsTotal * EUCountryVAT[strings.ToLower(taxAddress.Country)],
			NumberForInvoice: fmt.Sprintf("%s - %d%%", strings.ToUpper(taxAddress.Country), int(EUCountryVAT[strings.ToLower(taxAddress.Country)]*100)),
			Rate:             EUCountryVAT[strings.ToLower(taxAddress.Country)],
		})
	} else {
		taxes.Taxes = append(taxes.Taxes, snipcart.Tax{
			Name:             "NH Sales Tax",
			Amount:           0.0,
			NumberForInvoice: "0%",
			Rate:             0.0,
		})
	}

	DebugPrintf("finalized tax calculation for order %s", event.Content.Token)
	return &taxes, nil
}

func RouteSnipcartWebhook(shippoClient *shippo.Client, snipcartClient *snipcart.Client) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		validationHeader := c.GetHeader("X-Snipcart-RequestToken")
		if validationHeader == "" {
			c.AbortWithError(http.StatusBadRequest, errors.New("missing X-Snipcart-RequestToken header"))
			return
		}
		if err := snipcartClient.ValidateWebhook(validationHeader); err != nil {
			c.AbortWithError(http.StatusBadRequest, err)
			return
		}
		DebugPrintf("validated webhook '%s' successfully", validationHeader)

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
		/*
			case "order.completed":
				statusCode, err := HandleOrderComplete(io.NopCloser(bytes.NewBuffer(rawBody)), shippoClient)
				if err != nil {
					c.AbortWithError(statusCode, err)
					return
				}

				c.Data(statusCode, gin.MIMEHTML, nil)
		*/
		case "shippingrates.fetch":
			response, err := HandleShippingRates(ioutil.NopCloser(bytes.NewBuffer(rawBody)), shippoClient)
			if err != nil {
				logJsonWithStatus(JsonLogStatusError, "SHIPPING ERROR", err.Error())
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}

			c.JSON(http.StatusOK, response)
		case "taxes.calculate":
			response, err := HandleTaxCalculation(ioutil.NopCloser(bytes.NewBuffer(rawBody)))
			if err != nil {
				logJsonWithStatus(JsonLogStatusError, "TAX ERROR", err.Error())
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}

			c.JSON(http.StatusOK, response)
		default:
			logJsonWithStatus(JsonLogStatusWarning, "UNHANDLED EVENT", event.EventName)
			c.JSON(http.StatusOK, gin.H{})
		}

	}

	return fn
}

func init() {
	var err error
	webhookConfig, err = config.NewConfigFromEnv(false)
	if err != nil {
		DebugPrintf("[ERROR] %s", err.Error())
		return
	}

	shippoClient := shippo.NewClient(webhookConfig.ShippoApiKey)
	snipcartClient := snipcart.NewClient(webhookConfig.SnipcartApiKey)

	if webhookConfig.Production {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(jsonLoggerMiddleware())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "ready",
			"version": BuildVersion,
		})
	})
	r.POST("/webhooks/snipcart", RouteSnipcartWebhook(shippoClient, snipcartClient))

	ginLambda = ginadapter.New(r)
}

func Handler(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	return ginLambda.ProxyWithContext(ctx, req)
}

func main() {
	lambda.Start(Handler)
}
