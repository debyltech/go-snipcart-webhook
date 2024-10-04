package main

import (
	"fmt"
	"regexp"
	"sort"
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
	DebugPrintf("setting international info for order %s", order.Invoice)
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
				IssuingCountry: "ES",
				TaxId:          webhookConfig.IOSS,
				TaxIdType:      "IOSS",
			})
	}

	// TODO: Handle UK VAT
}

func DiscountedCost(shippingCost float64, discount int) float64 {
	discountedCost := shippingCost - float64(discount)

	if discountedCost < 0 {
		return 0.00
	}

	return discountedCost
}

// ShippingRateDescription takes the carrier, service, and estimated days to
// form a valid description by adding spaces to the upper camel case of the
// service level from EasyPost and appends an estimated days message if estimate
// provided
func ShippingRateDescription(carrier string, service string, estimatedDays int) string {
	// Regex to add and replace camelcase with spaces between camelcase words
	// for readability
	serviceRe := regexp.MustCompile(`([a-z])([A-Z])`)
	prettyService := serviceRe.ReplaceAllString(service, "$1 $2")

	description := fmt.Sprintf("%s %s", carrier, prettyService)

	if estimatedDays > 0 {
		// Add estimation to description if provided
		description = fmt.Sprintf("%s - Estimated arrival %d days", description, estimatedDays)
	}

	return description
}

// GenerateSnipcartRates takes a list of EasyPost shipping rates and returns an
// object with the list converted to what Snipcart expects as a return
// https://docs.snipcart.com/v3/webhooks/shipping
func GenerateSnipcartRates(config *config.Config, rates []*easypost.Rate) (*ShippingRatesResponse, error) {
	var ratesResponse ShippingRatesResponse

	for _, rate := range rates {
		// Skip disallowed rates
		if !config.CarrierAllowed(rate.Carrier) {
			continue
		}

		// Rate is provided as a string so float conversion is needed
		cost, err := strconv.ParseFloat(rate.Rate, 64)
		if err != nil {
			return nil, err
		}

		ratesResponse.Rates = append(ratesResponse.Rates, ShippingRate{
			Id:          rate.ID,
			Cost:        DiscountedCost(cost, config.ShippingDiscount),
			Description: ShippingRateDescription(rate.Carrier, rate.Service, rate.EstDeliveryDays),
		})
	}

	// Sort by lowest rate first
	sort.Slice(ratesResponse.Rates, func(i, j int) bool {
		return ratesResponse.Rates[i].Cost < ratesResponse.Rates[j].Cost
	})

	return &ratesResponse, nil
}
