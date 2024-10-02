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
	"strings"
	"time"

	"github.com/EasyPost/easypost-go/v4"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
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

// HandleShippingRates goes through the order and creates a shipment, running
// validations and adding information such as customs information on the way, or
// uses an existing shipment to respond with a list of rates for Snipcart
func HandleShippingRates(body io.ReadCloser, easypostClient *easypost.Client) (any, error) {
	var err error
	var event ShippingRateFetchWebhookEvent

	// Decode the incoming json and check validity
	if err := json.NewDecoder(body).Decode(&event); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with shipping rate fetch event decode: %s", err.Error())
	}

	logJson("shippingrates.fetch", event.Order.Token)

	// Validate Address Fields such as names being shorter than 2 letters, etc.
	if err := ValidateAddressFields(event.Order.ShippingAddress, webhookConfig.Production); err != nil {
		return err, nil
	}

	parcel := webhookConfig.DefaultParcel
	parcel.Weight = event.Order.TotalWeight

	shipment := easypost.Shipment{
		FromAddress: webhookConfig.SenderAddress,
		ToAddress: &easypost.Address{
			Name:    event.Order.ShippingAddress.Name,
			Company: event.Order.ShippingAddress.Company,
			Street1: event.Order.ShippingAddress.Address1,
			Street2: event.Order.ShippingAddress.Address2,
			City:    event.Order.ShippingAddress.City,
			Country: event.Order.ShippingAddress.Country,
			State:   event.Order.ShippingAddress.Province,
			Zip:     event.Order.ShippingAddress.PostalCode,
			Phone:   event.Order.ShippingAddress.Phone,
			Email:   event.Order.Email,
		},
		Parcel: parcel,
		TaxIdentifiers: []*easypost.TaxIdentifier{
			{
				Entity:         TAXENT_SENDER,
				TaxIdType:      "EIN",
				IssuingCountry: "US",
			},
		},
	}
	shipment.ReturnAddress = shipment.FromAddress

	// Set international info
	if IsInternational(event.Order.ShippingAddress.Country) {
		SetInternationalInfo(&shipment, &event.Order)
	}

	// Create the shipping response object when creating a shipment
	var shipmentResponse *easypost.Shipment

	// Check if we already have a shipment, otherwise create a shipment
	if event.Order.ShippingRateId != "" {
		shipmentId := strings.Split(event.Order.ShippingRateId, ";")[0]
		shipmentResponse, err = easypostClient.GetShipment(shipmentId)
		if err != nil {
			return http.StatusInternalServerError, fmt.Errorf("error with fetching existing shipment: %s", err.Error())
		}
	} else {
		DebugPrintf("creating shipment")
		shipmentResponse, err = easypostClient.CreateShipment(&shipment)
		if err != nil {
			return http.StatusInternalServerError, fmt.Errorf("error with creating shipment: %s", err.Error())
		}
	}

	// Check any carrier messages
	if len(shipmentResponse.Messages) > 0 {
		DebugPrintf("WARNING Shipment messages: %v", shipmentResponse.Messages)
	}

	// Generate shipping rates
	shippingRates, err := GenerateSnipcartRates(webhookConfig, shipmentResponse.Rates)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with creating shipment: %s", err.Error())
	}

	logJson("shippingrates.fetch", fmt.Sprintf("completed for %s", event.Order.Token))

	return shippingRates, nil
}

// HandleOrderComplete handles the completion of the order and simply creates a
// log message and a response code for Snipcart
// TODO: Is this being used at all?
func HandleOrderComplete(body io.ReadCloser) (int, error) {
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

// HandleTaxCalculation returns a list of taxes that need to be applied to an
// existing order as part of checkout for customers. This primarily has to do
// with international Value Added Tax, but may pertain to sales tax as well.
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
		// TODO: Make this customizable in the future? Not everyone is from NH
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

// RouteSnipcartWebhook routes the webhook request, after validating the
// Snipcart RequestToken, to it's relevant location (i.e. tax, order complete,
// etc.)
func RouteSnipcartWebhook(easypostClient *easypost.Client, snipcartClient *snipcart.Client) gin.HandlerFunc {
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
			// TODO: Why does this no longer exist?
			case "order.completed":
				statusCode, err := HandleOrderComplete(io.NopCloser(bytes.NewBuffer(rawBody)), easypostClient)
				if err != nil {
					c.AbortWithError(statusCode, err)
					return
				}

				c.Data(statusCode, gin.MIMEHTML, nil)
		*/
		case "shippingrates.fetch":
			response, err := HandleShippingRates(ioutil.NopCloser(bytes.NewBuffer(rawBody)), easypostClient)
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

	easypostClient := easypost.New(webhookConfig.EasypostApiKey)
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
	r.POST("/webhooks/snipcart", RouteSnipcartWebhook(easypostClient, snipcartClient))

	ginLambda = ginadapter.New(r)
}

func Handler(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	return ginLambda.ProxyWithContext(ctx, req)
}

func main() {
	lambda.Start(Handler)
}
