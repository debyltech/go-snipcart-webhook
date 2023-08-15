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
	EventName string    `json:"eventName"`
	CreatedOn time.Time `json:"createdOn"`
	Order     any       `json:"content"`
}

type ShippingRate struct {
	Id          string  `json:"userDefinedId"`
	Cost        float64 `json:"cost"`
	Description string  `json:"description"`
	// We cannot guarantee days to delivery, kept here for debug future
	// DeliveryDays int     `json:"guaranteedDaysToDelivery"`
}

type ShippingRatesResponse struct {
	Rates []ShippingRate `json:"rates"`
}

func HandleShippingRates(body io.ReadCloser, shippoClient *shippo.Client) (any, error) {
	var err error
	var event ShippingRateFetchWebhookEvent
	if err := json.NewDecoder(body).Decode(&event); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with shipping rate fetch event decode: %s", err.Error())
	}

	if webhookConfig.Production {
		var valid bool

		// Ensure the name of the shipping address has at least two words
		shippingAddressName := strings.Split(event.Order.ShippingAddress.Name, " ")
		valid = len(shippingAddressName) > 1

		if !valid {
			return snipcart.ShippingErrors{
				Errors: []snipcart.ShippingError{
					{
						Key:     "invalid_address_name",
						Message: "Shipping Address name must be at least two words (ex. 'Jon D', 'Jon Doe')",
					},
				},
			}, nil
		}

		// Ensure the first name of the shipping address has more than two characters
		valid = len(shippingAddressName[0]) > 2
		if !valid {
			return snipcart.ShippingErrors{
				Errors: []snipcart.ShippingError{
					{
						Key:     "invalid_address_firstname_length",
						Message: "Shipping Address first name must be longer than two characters (ex: 'Jon')",
					},
				},
			}, nil
		}

		// Validate address
		valid, err = shippoClient.ValidateAddress(shippo.Address{
			Name:       event.Order.ShippingAddress.Name,
			Address1:   event.Order.ShippingAddress.Address1,
			City:       event.Order.ShippingAddress.City,
			Country:    event.Order.ShippingAddress.Country,
			State:      event.Order.ShippingAddress.Province,
			PostalCode: event.Order.ShippingAddress.PostalCode,
			Email:      event.Order.Email,
		})
		if err != nil {
			return http.StatusInternalServerError, err
		}

		if !valid {
			return snipcart.ShippingErrors{
				Errors: []snipcart.ShippingError{
					{
						Key:     "invalid_address",
						Message: "The address entered is not valid!",
					},
				},
			}, nil
		}
	}

	var lineItems []shippo.LineItem
	var customsItemIds []string
	for _, v := range event.Order.Items {
		if !v.Shippable {
			DebugPrintf("order %s item %s not shippable, skipping\n", event.Order.Token, v.Name)
			continue
		}

		lineItems = append(lineItems, shippo.LineItem{
			Quantity:           v.Quantity,
			TotalPrice:         fmt.Sprintf("%.2f", v.TotalPrice),
			Currency:           strings.ToUpper(event.Order.Currency),
			Weight:             fmt.Sprintf("%.2f", v.Weight),
			WeightUnit:         webhookConfig.WeightUnit,
			Title:              v.Name,
			ManufactureCountry: webhookConfig.ManufactureCountry,
			Sku:                v.ID,
		})

		if strings.ToLower(event.Order.Country) != "us" {
			// International
			customsItem := shippo.CustomsItem{
				Description:   v.Name,
				Quantity:      v.Quantity,
				NetWeight:     fmt.Sprintf("%.2f", v.Weight),
				WeightUnit:    webhookConfig.WeightUnit,
				Currency:      strings.ToUpper(event.Order.Currency),
				ValueAmount:   fmt.Sprintf("%.2f", v.TotalPrice),
				OriginCountry: webhookConfig.SenderAddress.Country,
				Metadata:      fmt.Sprintf("order:%s", event.Order.Invoice),
			}

			// Handle tariff numbers
			for _, f := range v.CustomFields {
				if f.Name == "hs_code" {
					customsItem.TariffNumber = f.Value
				}
			}

			createCustomsItemResponse, err := shippoClient.CreateCustomsItem(customsItem)
			if err != nil {
				return http.StatusInternalServerError, fmt.Errorf("error with creating customs item: %s", err.Error())
			}

			customsItemIds = append(customsItemIds, createCustomsItemResponse.Id)
		}
	}

	DebugPrintf("handled %d line items\n", len(lineItems))
	if len(lineItems) <= 0 {
		return http.StatusOK, nil
	}

	var customsDeclaration *shippo.CustomsDeclaration
	if strings.ToLower(event.Order.Country) != "us" {
		declaration := shippo.CustomsDeclaration{
			Certify:           true,
			CertifySigner:     webhookConfig.CustomsVerifier,
			Items:             customsItemIds,
			NonDeliveryOption: shippo.NONDELIV_RETURN,
			ContentsType:      shippo.CONTYP_MERCH,
			Incoterm:          shippo.INCO_DDU,
			ExporterIdentification: shippo.ExporterIdentification{
				TaxId: shippo.CustomsTaxId{
					Number: webhookConfig.EIN,
					Type:   shippo.TAX_EIN,
				},
			},
			InvoicedCharges: shippo.InvoicedCharges{
				Currency:      strings.ToUpper(event.Order.Currency),
				TotalShipping: fmt.Sprintf("%.2f", event.Order.ShippingCost),
				TotalTaxes:    fmt.Sprintf("%.2f", event.Order.TotalTaxes),
				TotalDuties:   "0.00",
				OtherFees:     "0.00",
			},
		}

		DebugPrintf("international country detected: %s\n", event.Order.Country)

		/* Handle special Canadial EEL/PFC */
		if strings.ToLower(event.Order.Country) == "ca" {
			declaration.EELPFC = shippo.EEL_NOEEI3036
		} else {
			declaration.EELPFC = shippo.EEL_NOEEI3037a
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

	parcel := webhookConfig.DefaultParcel
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
		shipmentRequest.CustomsDeclaration = customsDeclaration.Id
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
		DebugPrintf("creating shipment\n")
		shipmentResponse, err = shippoClient.CreateShipment(shipmentRequest)
		if err != nil {
			return http.StatusInternalServerError, fmt.Errorf("error with creating shipment: %s", err.Error())
		}
	}

	if len(shipmentResponse.Messages) > 0 {
		DebugPrintf("WARNING Shipment messages: %v\n", shipmentResponse.Messages)
	}

	DebugPrintf("awaiting shipment creation succeessful...\n")
	err = shippoClient.AwaitQueuedFinished(shipmentResponse.Id)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with awaiting shipment status: %s\n", err.Error())
	}

	ratesResponse, err := shippoClient.GetRatesForShipmentId(shipmentResponse.Id)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with getting rates: %s", err.Error())
	}
	DebugPrintf("got successful shipping rate response from shippo\n")

	DebugPrintf("creating shipping rates for snipcart...\n")
	var shippingRates ShippingRatesResponse
	for _, rate := range ratesResponse.Rates {
		if !webhookConfig.ServiceLevelAllowed(rate.ServiceLevel.Token) {
			DebugPrintf("shipping rate service level '%s' skipped\n", rate.ServiceLevel.Token)
			continue
		}

		cost, err := strconv.ParseFloat(rate.Amount, 64)
		if err != nil {
			return nil, fmt.Errorf("error with parsing float in shipping rates: %s", err.Error())
		}

		DebugPrintf("adding shipping service level '%s' costing '%.2f'\n", rate.ServiceLevel.Name, cost)
		shippingRates.Rates = append(shippingRates.Rates, ShippingRate{
			Id:          fmt.Sprintf("%s;%s", shipmentResponse.Id, rate.Id),
			Cost:        cost,
			Description: fmt.Sprintf("%s %s - Estimated arrival in %d days", rate.Provider, rate.ServiceLevel.Name, rate.EstimatedDays),
		})
	}

	DebugPrintf("successful shipping rate response\n")

	return shippingRates, nil
}

func HandleOrderComplete(body io.ReadCloser, shippoClient *shippo.Client) (int, error) {
	var event OrderCompleteWebhookEvent
	if err := json.NewDecoder(body).Decode(&event); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with ordercomplete event decode: %s", err.Error())
	}

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

	var taxAddress *snipcart.Address = &event.Content.ShippingAddress

	if event.Content.ShipToBillingAddress {
		taxAddress = &event.Content.BillingAddress
	}

	DebugPrintf("successfully decoded webhook tax POST content -- state %s country %s\n", taxAddress.Province, taxAddress.Country)

	/* Tax - EU */
	if IsEUCountry(taxAddress.Country) {
		DebugPrintf("detected EU country for Tax calculation: %s\n", taxAddress.Country)

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

	DebugPrintf("finalized tax calculation for order %s\n", event.Content.Token)
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
		DebugPrintf("validated webhook '%s' successfully\n", validationHeader)

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
			fmt.Printf("handling event: %s\n", event.EventName)
			statusCode, err := HandleOrderComplete(ioutil.NopCloser(bytes.NewBuffer(rawBody)), shippoClient)
			if err != nil {
				c.AbortWithError(statusCode, err)
				return
			}

			c.Data(statusCode, gin.MIMEHTML, nil)
		case "shippingrates.fetch":
			fmt.Printf("handling event: %s\n", event.EventName)
			response, err := HandleShippingRates(ioutil.NopCloser(bytes.NewBuffer(rawBody)), shippoClient)
			if err != nil {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}

			c.JSON(http.StatusOK, response)
		case "taxes.calculate":
			fmt.Printf("handling event: %s\n", event.EventName)
			response, err := HandleTaxCalculation(ioutil.NopCloser(bytes.NewBuffer(rawBody)))
			if err != nil {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}

			c.JSON(http.StatusOK, response)
		default:
			fmt.Printf("unhandled event: %s\n", event.EventName)
			c.JSON(http.StatusOK, gin.H{})
		}

	}

	return fn
}

func init() {
	var err error
	webhookConfig, err = config.NewConfigFromEnv(false)
	if err != nil {
		DebugPrintf("[ERROR] %s\n", err.Error())
		return
	}

	shippoClient := shippo.NewClient(webhookConfig.ShippoApiKey)
	snipcartClient := snipcart.NewClient(webhookConfig.SnipcartApiKey)

	if webhookConfig.Production {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()

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
