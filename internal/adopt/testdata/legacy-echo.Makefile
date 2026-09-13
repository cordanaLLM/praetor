.PHONY: all verify-all audit compile-context build test

verify-all:
	@echo "Running verification..."

compile-context:
	@standardsctl compile-context

audit:
	@standardsctl audit

test:
	@go test -v -race ./...

build:
	@go build -v ./...
