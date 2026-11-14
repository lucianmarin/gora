GO ?= go
PREFIX ?= $(shell brew --prefix 2>/dev/null || echo /usr/local)
BINDIR ?= $(DESTDIR)$(PREFIX)/bin

TARGET = goa

.PHONY: all test bench install uninstall clean

all: $(TARGET)

$(TARGET): *.go go.mod
	$(GO) build -o $(TARGET) .

test: $(TARGET)
	$(GO) test -v ./...
	./$(TARGET) --test

bench: $(TARGET)
	./$(TARGET) --bench

install: $(TARGET)
	install -d "$(BINDIR)"
	install -m 755 $(TARGET) "$(BINDIR)/$(TARGET)"

uninstall:
	rm -f "$(BINDIR)/$(TARGET)"

clean:
	rm -f $(TARGET)
