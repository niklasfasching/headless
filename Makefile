.PHONY: install
install:
	go env -w GOPROXY=direct
	go get -u ./...

.PHONY: test
test:
	go test -v ./...
