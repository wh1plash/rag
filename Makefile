-include .env
export $(shell [ -f .env ] && sed 's/=.*//' .env)

build_loader:
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o loader/bin/loader loader/cmd/main.go

run: build_loader
	@./loader/bin/loader

.PHONY: build_loader