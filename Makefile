.PHONY: all build run clean populate

all: build

build:
	go build -o proxy-api-bin ./cmd/proxy-api
	go build -o populate-usage-stats-bin ./cmd/populate-usage-stats
	go build -o populate-format-stats-bin ./cmd/populate-format-stats
	go build -o populate-leads-bin ./cmd/populate-leads
	go build -o populate-metagame-bin ./cmd/populate-metagame
	go build -o populate-viability-bin ./cmd/populate-viability
	go build -o preload-dbcache-bin ./cmd/preload-dbcache
	go build -o warmup-bin ./cmd/warmup

run: build
	./proxy-api-bin

populate: build
	./populate-usage-stats-bin
	./populate-format-stats-bin
	./populate-leads-bin
	./populate-metagame-bin
	./populate-viability-bin

clean:
	rm -f proxy-api-bin
	rm -f populate-usage-stats-bin
	rm -f populate-format-stats-bin
	rm -f populate-leads-bin
	rm -f populate-metagame-bin
	rm -f populate-viability-bin
	rm -f preload-dbcache-bin
	rm -f warmup-bin
