package main

import (
	"encoding/json"
	"fmt"
	"strings"

	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/debyltech/go-snipcart-webhook/config"
	"github.com/gin-gonic/gin"
)

type JsonLogStatus string

type JsonLog struct {
	Status  JsonLogStatus `json:"status,omitempty"`
	Event   string        `json:"event"`
	Message string        `json:"message"`
}

const (
	ValidateUrl string = "https://app.snipcart.com/api/requestvalidation/"

	JsonLogStatusOk      JsonLogStatus = "ok"
	JsonLogStatusWarning JsonLogStatus = "warning"
	JsonLogStatusError   JsonLogStatus = "error"
)

var (
	ginLambda     *ginadapter.GinLambda
	webhookConfig *config.Config

	BuildVersion string             = "development"
	EUCountryVAT map[string]float64 = map[string]float64{
		"at": 0.20, // Austria
		"be": 0.21, // Belgium
		"bg": 0.20, // Bulgaria
		"hr": 0.25, // Croatia
		"cy": 0.19, // Cyprus
		"cz": 0.21, // Czech Republic
		"dk": 0.25, // Denmark
		"ee": 0.20, // Estonia
		"fi": 0.24, // Finland
		"fr": 0.20, // France
		"de": 0.19, // Germany
		"gr": 0.24, // Greece
		"hu": 0.27, // Hungary
		"ie": 0.23, // Ireland, Republic of (EIRE)
		"it": 0.22, // Italy
		"lv": 0.21, // Latvia
		"lt": 0.21, // Lithuania
		"lu": 0.16, // Luxembourg
		"mt": 0.18, // Malta
		"nl": 0.21, // Netherlands
		"pl": 0.23, // Poland
		"pt": 0.23, // Portugal
		"ro": 0.19, // Romania
		"sk": 0.20, // Slovakia
		"si": 0.22, // Slovenia
		"es": 0.21, // Spain
		"se": 0.25, // Sweden
	}
)

func IsEUCountry(countryCode string) bool {
	switch strings.ToLower(countryCode) {
	case
		"at", // Austria
		"be", // Belgium
		"bg", // Bulgaria
		"hr", // Croatia
		"cy", // Cyprus
		"cz", // Czech Republic
		"dk", // Denmark
		"ee", // Estonia
		"fi", // Finland
		"fr", // France
		"de", // Germany
		"gr", // Greece
		"hu", // Hungary
		"ie", // Ireland, Republic of (EIRE)
		"it", // Italy
		"lv", // Latvia
		"lt", // Lithuania
		"lu", // Luxembourg
		"mt", // Malta
		"nl", // Netherlands
		"pl", // Poland
		"pt", // Portugal
		"ro", // Romania
		"sk", // Slovakia
		"si", // Slovenia
		"es", // Spain
		"se": // Sweden
		return true
	}

	return false
}

func jsonLoggerMiddleware() gin.HandlerFunc {
	return gin.LoggerWithFormatter(
		func(params gin.LogFormatterParams) string {
			log := make(map[string]interface{})

			log["status_code"] = params.StatusCode
			log["path"] = params.Path
			log["method"] = params.Method
			log["start_time"] = params.TimeStamp.Format("2006/01/02 - 15:04:05")
			log["response_time"] = params.Latency.String()

			s, _ := json.Marshal(log)
			return string(s) + "\n"
		},
	)
}

func logJsonWithStatus(status JsonLogStatus, event string, message string) {
	logBytes, _ := json.Marshal(JsonLog{
		Status:  status,
		Event:   event,
		Message: message,
	})
	fmt.Println(string(logBytes))
}

func logJson(event string, message string) {
	logJsonWithStatus(JsonLogStatusOk, event, message)
}
