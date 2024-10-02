package main

import (
	"fmt"
	"strings"

	"github.com/EasyPost/easypost-go/v4"
	"github.com/debyltech/go-snipcart/snipcart"
)

// TODO: Create func to wrap customs items getting

func GenerateCustomsItems(order snipcart.Order) []*easypost.CustomsItem {
	var customsItems []*easypost.CustomsItem

	for _, v := range order.Items {
		if !v.Shippable {
			logJson("shippingratches.fetch", fmt.Sprintf("order %s item %s not shippable, skipping", event.Order.Token, v.Name))
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
