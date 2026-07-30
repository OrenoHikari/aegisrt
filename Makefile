.PHONY: run build test fmt clean

run:
	go run ./cmd/aegisd

build:
	mkdir -p bin
	go build -o bin/capsulertd ./cmd/aegisd

test:
	go test ./...

fmt:
	gofmt -w cmd internal

clean:
	rm -rf bin
	rm -f logs/events.jsonl
