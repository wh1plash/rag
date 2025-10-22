-include .env
export $(shell [ -f .env ] && sed 's/=.*//' .env)

build_loader:
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o loader/bin/loader loader/cmd/main.go

run_loader: build_loader
	@./loader/bin/loader

build_app:
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o app/bin/server app/cmd/main.go

run_app: build_app
	@./app/bin/server

.PHONY: build_loader, build_app