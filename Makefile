.PHONY: tidy build test vet fmt run

tidy:
	GOPROXY=direct go mod tidy

build: tidy
	CGO_ENABLED=1 go build -ldflags="-X 'github.com/isocroft/tallaq/debugging.enabledFlag=true'" -o bin/tallaq ./cmd/techdebt

test:
	go test $$(go list ./... | grep -viE '/parse|/gitsifter|/health|/clumper|/techdebt')

vet:
	go vet ./...

fmt:
	gofmt -s -w .

run:
	CGO_ENABLED=1 go run ./cmd/techdebt $(ARGS)
