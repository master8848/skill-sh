BINARY := mskill

.PHONY: build vet test clean

build:
	go build -o $(BINARY) .

vet:
	go vet ./...

test:
	go test ./...

clean:
	rm -f $(BINARY) $(BINARY)-*
