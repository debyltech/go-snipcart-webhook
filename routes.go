package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/gin-gonic/gin"
)

func RouteSnipcartWebhook() gin.HandlerFunc {
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
			DebugPrintf("handling event: %s\n", event.EventName)
			statusCode, err := HandleOrderComplete(ioutil.NopCloser(bytes.NewBuffer(rawBody)))
			if err != nil {
				c.AbortWithError(statusCode, err)
				return
			}

			c.Data(statusCode, gin.MIMEHTML, nil)
		case "shippingrates.fetch":
			DebugPrintf("handling event: %s\n", event.EventName)
			response, err := HandleShippingRates(ioutil.NopCloser(bytes.NewBuffer(rawBody)))
			if err != nil {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}

			c.JSON(http.StatusOK, response)
		case "taxes.calculate":
			DebugPrintf("handling event: %s\n", event.EventName)
			response, err := HandleTaxCalculation(ioutil.NopCloser(bytes.NewBuffer(rawBody)))
			if err != nil {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}

			c.JSON(http.StatusOK, response)
		default:
			DebugPrintf("unhandled event: %s\n", event.EventName)
		}

	}

	return fn
}
