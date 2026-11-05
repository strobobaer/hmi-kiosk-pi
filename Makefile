.PHONY: test vet build

test:
	scripts/dgo test -race ./...

vet:
	scripts/dgo vet ./...

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 scripts/dgo build ./...
