package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/debyltech/go-shippr/shippo"
	"github.com/debyltech/go-snipcart/snipcart"
)

type ShippingRateFetchWebhookEvent struct {
	EventName string                 `json:"eventName"`
	CreatedOn time.Time              `json:"createdOn"`
	Order     snipcart.SnipcartOrder `json:"content"`
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

func HandleShippingRates(body io.ReadCloser) (any, error) {
	var event ShippingRateFetchWebhookEvent
	if err := json.NewDecoder(body).Decode(&event); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("error with shipping rate fetch event decode: %s", err.Error())
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
			customsItem, err := shippoClient.CreateCustomsItem(shippo.CustomsItem{
				Description:   v.Name,
				Quantity:      v.Quantity,
				NetWeight:     fmt.Sprintf("%.2f", v.Weight),
				WeightUnit:    webhookConfig.WeightUnit,
				Currency:      strings.ToUpper(event.Order.Currency),
				ValueAmount:   fmt.Sprintf("%.2f", v.TotalPrice),
				OriginCountry: webhookConfig.SenderAddress.Country,
				Metadata:      fmt.Sprintf("order:%s", event.Order.Invoice),
			})
			if err != nil {
				return http.StatusInternalServerError, fmt.Errorf("error with creating customs item: %s", err.Error())
			}

			customsItemIds = append(customsItemIds, customsItem.Id)
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
		}

		DebugPrintf("international country detected: %s\n", event.Order.Country)

		if strings.ToLower(event.Order.Country) == "ca" {
			declaration.EELPFC = shippo.EEL_NOEEI3036
		} else {
			declaration.EELPFC = shippo.EEL_NOEEI3037a
		}

		// TODO(bastian): Add the TaxId override for EU/UK

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

	var err error
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
