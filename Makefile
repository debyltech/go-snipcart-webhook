SRC=$(wildcard *.go)

go-snipcart-webhook: $(SRC)
	CGO_ENABLED=0 go build -tags netgo -o bootstrap

go-snipcart-webhook.zip: go-snipcart-webhook
	zip go-snipcart-webhook.zip bootstrap

deploy-dev: go-snipcart-webhook.zip
	aws --profile debyltech lambda update-function-code --function-name 'webhooks-dev' --zip-file 'fileb://go-snipcart-webhook.zip'

deploy-prod: go-snipcart-webhook.zip
	aws --profile debyltech lambda update-function-code --function-name 'webhooks-prod' --zip-file 'fileb://go-snipcart-webhook.zip'

.PHONY: go-snipcart-webhook go-snipcart-webhook.zip