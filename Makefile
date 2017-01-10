.PHONY: build
build: github-mirror

.PHONY: update-vendored
update-vendored:
	glide install --strip-vendor --skip-test
	find vendor -name '*_test.go' -delete

github-mirror: *.go
	go build
