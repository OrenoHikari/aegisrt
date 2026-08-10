.PHONY: run build test fmt clean dashboard dashboard-smoke dashboard-screenshot experiment-demo research-python-setup research-smoke

run:
	go run ./cmd/aegisd

build:
	mkdir -p bin
	go build -o bin/capsulertd ./cmd/aegisd
	go build -o bin/capsulectl ./cmd/aegisctl

test:
	go test ./...

fmt:
	gofmt -w cmd internal

dashboard: build
	./bin/capsulectl dashboard --mock

dashboard-smoke: build
	bash scripts/dashboard-smoke.sh --skip-build

dashboard-screenshot: build
	bash scripts/dashboard-screenshot.sh --skip-build

experiment-demo: build
	./bin/capsulectl experiment demo

research-python-setup:
	python3 -m venv .venv-research
	.venv-research/bin/python -m pip install -r worker/python/requirements-research.txt

research-smoke: build
	./bin/capsulectl agent research-smoke --python .venv-research/bin/python

clean:
	rm -rf bin
	rm -f logs/events.jsonl
