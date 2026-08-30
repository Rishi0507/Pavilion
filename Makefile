# Pavilion build targets.
#
# Every generated artifact must be reproducible from raw data by one target.
# On Windows without make on PATH, use mingw32-make.

GO      ?= go
RAW     := data/raw
OUT     := data/out
MATCHES := $(RAW)/ipl_json

.PHONY: all data etl attrs rates features model winprob puzzles sweep serve dev play check baseline bench test lint clean

all: etl attrs rates features model winprob test

## data: download the Cricsheet IPL archive and the people register
data:
	@mkdir -p $(RAW)
	curl -fSL -o $(RAW)/ipl_json.zip https://cricsheet.org/downloads/ipl_json.zip
	curl -fSL -o $(RAW)/people.csv   https://cricsheet.org/register/people.csv
	@mkdir -p $(MATCHES)
	cd $(MATCHES) && unzip -oq ../ipl_json.zip

## etl: build the binary corpus and the data quality report
etl: $(OUT)/corpus.bin

$(OUT)/corpus.bin: $(wildcard $(MATCHES)/*.json) $(RAW)/people.csv $(wildcard cmd/pavetl/*.go) $(wildcard internal/corpus/*.go)
	$(GO) run ./cmd/pavetl -matches $(MATCHES) -register $(RAW)/people.csv -out $(OUT)

## attrs: resolve batting handedness and bowling type for the eligible players
attrs: $(OUT)/corpus.bin
	$(GO) run ./cmd/pavattr

## rates: fit the hierarchical shrunk player rate table
rates: $(OUT)/rates.bin

$(OUT)/rates.bin: $(OUT)/corpus.bin data/attributes/players.csv $(wildcard internal/rates/*.go)
	$(GO) run ./cmd/pavrates

## features: export the training matrix for the outcome model
features: $(OUT)/train.csv

$(OUT)/train.csv: $(OUT)/corpus.bin data/attributes/players.csv $(wildcard internal/features/*.go)
	$(GO) run ./cmd/pavfeat

## model: train and calibrate the ball outcome model
model: data/models/outcome.txt

data/models/outcome.txt: $(OUT)/train.csv ml/train.py ml/pyproject.toml
	cd ml && uv run python train.py

## winprob: train the win probability model
winprob: data/models/winprob.txt

data/models/winprob.txt: $(OUT)/wp_train.csv ml/train_wp.py
	cd ml && uv run python train_wp.py

## puzzles: generate and validate the daily puzzle queue
puzzles: data/models/winprob.txt
	$(GO) run ./cmd/pavpuzzle -days 7 -candidates 12

## sweep: print how targets and attacks behave, for calibrating the criteria
sweep:
	$(GO) run ./cmd/pavpuzzle -sweep -games 400

## serve: run the game on localhost
serve:
	$(GO) run ./cmd/pavsrv -addr 127.0.0.1:8080

## dev: same, but serve the frontend from disk so a refresh picks up edits
dev:
	$(GO) run ./cmd/pavsrv -addr 127.0.0.1:8080 -dev

## play: play today's puzzle at a terminal
play:
	$(GO) run ./cmd/pavplay

## check: rebuild the report and fail on any data quality regression
check:
	$(GO) run ./cmd/pavetl -check

## baseline: accept the current report as the new quality baseline
baseline:
	$(GO) run ./cmd/pavetl -write-baseline

## test: run the full test suite
test:
	$(GO) test ./...

## bench: benchmark the aggregation and graph hot paths
bench:
	$(GO) test -bench=. -benchtime=200x -run='^$$' ./internal/corpus/ ./internal/graph/

## lint: vet and formatting check
lint:
	$(GO) vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

## clean: remove generated artifacts, keeping raw downloads
clean:
	rm -f $(OUT)/corpus.bin $(OUT)/rates.bin $(OUT)/train.csv $(OUT)/test.csv $(OUT)/wp_train.csv $(OUT)/wp_test.csv
