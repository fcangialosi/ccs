.PHONY: build install clean check

build:
	go build -o ccs .

install: build
	mkdir -p ~/.local/bin
	cp ccs ~/.local/bin/ccs

clean:
	rm -f ccs

check:
	go vet ./...
	go build ./...
