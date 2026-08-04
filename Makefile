.PHONY: all build run clean populate

all: build

build:
	go build -o proxy-api-bin ./cmd/proxy-api
	go build -o populate-all-bin ./cmd/populate-all
	go build -o preload-dbcache-bin ./cmd/preload-dbcache
	go build -o warmup-bin ./cmd/warmup

run: build
	./proxy-api-bin

populate: build
	./populate-all-bin all

clean:
	rm -f proxy-api-bin
	rm -f populate-all-bin
	rm -f preload-dbcache-bin
	rm -f warmup-bin
