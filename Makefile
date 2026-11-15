.PHONY: tidy build test vet fmt run

tidy:
	GOPROXY=direct go mod tidy

build: tidy
	go build -o bin/tallaq ./cmd/techdebt

test:
	go test $$(go list ./... | grep -viE '/parse|/gitsifter|/clumper|/callgraph|/techdebt')

vet:
	go vet ./...

fmt:
	gofmt -s -w .

run:
	go run ./cmd/techdebt $(ARGS)
