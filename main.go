package main

import (
	"context"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/debyltech/go-shippr/shippo"
	"github.com/debyltech/go-snipcart-webhook/config"
	"github.com/debyltech/go-snipcart/snipcart"
	"github.com/gin-gonic/gin"
)

func init() {
	var err error
	webhookConfig, err = config.NewConfigFromEnv(true)
	if err != nil {
		DebugPrintf("[ERROR] %s\n", err.Error())
		return
	}

	shippoClient = shippo.NewClient(webhookConfig.ShippoApiKey)
	snipcartClient = snipcart.NewClient(webhookConfig.SnipcartApiKey)

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
	r.POST("/webhooks/snipcart", RouteSnipcartWebhook())

	ginLambda = ginadapter.New(r)
}

func Handler(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	return ginLambda.ProxyWithContext(ctx, req)
}

func main() {
	lambda.Start(Handler)
}
