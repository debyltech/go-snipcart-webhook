package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/EasyPost/easypost-go/v4"
	"github.com/debyltech/go-snipcart-webhook/config"
	"github.com/debyltech/go-snipcart/snipcart"
)

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

func GenerateCustomsItems(order *snipcart.Order) []*easypost.CustomsItem {
	var customsItems []*easypost.CustomsItem

	for _, v := range order.Items {
		if !v.Shippable {
			logJson("shippingratches.fetch", fmt.Sprintf("order %s item %s not shippable, skipping", order.Token, v.Name))
			continue
		}

		if strings.ToLower(order.Country) != "us" {
			// International
			customsItem := easypost.CustomsItem{
				Description:   v.Name,
				Quantity:      float64(v.Quantity),
				Weight:        v.Weight,
				Value:         v.TotalPrice,
				OriginCountry: webhookConfig.SenderAddress.Country,
				Code:          order.Invoice,
				Currency:      order.Currency,
			}

			// Handle tariff numbers
			for _, f := range v.CustomFields {
				if f.Name == "hs_code" {
					customsItem.HSTariffNumber = f.Value
				}
			}

			customsItems = append(customsItems, &customsItem)
		}
	}

	return customsItems
}

func SetInternationalInfo(shipment *easypost.Shipment, order *snipcart.Order) {
	// TODO: move this to a func
	shipment.CustomsInfo = &easypost.CustomsInfo{
		CustomsCertify:    true,
		CustomsSigner:     webhookConfig.CustomsVerifier,
		RestrictionType:   RSTRCTTYP_NONE,
		EELPFC:            EEL_NOEEI3037a,
		CustomsItems:      GenerateCustomsItems(order),
		NonDeliveryOption: NONDELIV_RETURN, // TODO: do we ever want to abandon?
		ContentsType:      CONTYP_MERCH,
	}

	/* Handle special Canadian EEL/PFC */
	if strings.ToLower(order.Country) == "ca" {
		shipment.CustomsInfo.EELPFC = EEL_NOEEI3036
	}

	/* Handle EU IOSS */
	if IsEUCountry(order.Country) {
		shipment.TaxIdentifiers = append(shipment.TaxIdentifiers,
			&easypost.TaxIdentifier{
				Entity:         TAXENT_SENDER,
				TaxIdType:      "IOSS",
				IssuingCountry: "ES",
			})
	}

	// TODO: Handle UK VAT
}

func GenerateSnipcartRates(config *config.Config, rates []*easypost.Rate) (*ShippingRatesResponse, error) {
	var ratesResponse ShippingRatesResponse

	for _, rate := range rates {
		// Skip disallowed rates
		if !config.ServiceLevelAllowed(rate.Carrier, rate.Service) {
			continue
		}

		cost, err := strconv.ParseFloat(rate.Rate, 32)
		if err != nil {
			return nil, err
		}

		ratesResponse.Rates = append(ratesResponse.Rates, ShippingRate{
			Id:          rate.ID,
			Cost:        cost,
			Description: fmt.Sprintf("%s %s - Estimated arrival in %d days", rate.Carrier, rate.Service, rate.EstDeliveryDays),
		})
	}

	return &ratesResponse, nil
}
