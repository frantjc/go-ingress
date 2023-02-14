GO = go
GOLANGCI-LINT = golangci-lint

.DEFAULT: test

fmt test:
	@$(GO) $@ ./...

download vendor verify:
	@$(GO) mod $@

lint:
	@$(GOLANGCI-LINT) run --fix

gen: generate
dl: download
ven: vendor
ver: verify
format: fmt

.PHONY: fmt test download vendor verify lint dl ven ver format
