# Manhattan build targets.
#
# Every generated artifact must be reproducible from raw data by one target.
# On Windows without make on PATH, use mingw32-make.

GO      ?= go
RAW     := data/raw
OUT     := data/out
MATCHES := $(RAW)/ipl_json

.PHONY: all data etl attrs check test lint clean

all: etl attrs test

## data: download the Cricsheet IPL archive and the people register
data:
	@mkdir -p $(RAW)
	curl -fSL -o $(RAW)/ipl_json.zip https://cricsheet.org/downloads/ipl_json.zip
	curl -fSL -o $(RAW)/people.csv   https://cricsheet.org/register/people.csv
	@mkdir -p $(MATCHES)
	cd $(MATCHES) && unzip -oq ../ipl_json.zip

## etl: build the binary corpus and the data quality report
etl: $(OUT)/corpus.bin

$(OUT)/corpus.bin: $(wildcard $(MATCHES)/*.json) $(RAW)/people.csv $(wildcard cmd/paretl/*.go) $(wildcard internal/corpus/*.go)
	$(GO) run ./cmd/paretl -matches $(MATCHES) -register $(RAW)/people.csv -out $(OUT)

## attrs: resolve batting handedness and bowling type for the eligible players
attrs: $(OUT)/corpus.bin
	$(GO) run ./cmd/parattr

## check: rebuild the report and fail on any data quality regression
check:
	$(GO) run ./cmd/paretl -check

## baseline: accept the current report as the new quality baseline
baseline:
	$(GO) run ./cmd/paretl -write-baseline

## test: run the full test suite
test:
	$(GO) test ./...

## lint: vet and formatting check
lint:
	$(GO) vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

## clean: remove generated artifacts, keeping raw downloads
clean:
	rm -f $(OUT)/corpus.bin
