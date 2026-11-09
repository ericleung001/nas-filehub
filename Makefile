.PHONY: build run smoke tidy
build:
	go build -o nas-filehub ./cmd/server
run: build
	DATA_ROOT=./data/files DB_PATH=./data/filehub.db THUMB_CACHE_DIR=./data/thumbs \
	ADMIN_USER=admin ADMIN_PASSWORD=$${ADMIN_PASSWORD:-changeme} LISTEN_ADDR=:8080 ./nas-filehub
smoke:
	./scripts/smoke.sh
tidy:
	go mod tidy

.PHONY: gen-env
gen-env:
	./scripts/gen-env.sh
