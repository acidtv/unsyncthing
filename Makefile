GOMOBILE   ?= gomobile
AAR        := android/app/libs/stclient.aar
STCLIENT   := ./stclient

.PHONY: all gomobile android tidy test clean

all: gomobile

## Build the gomobile AAR and drop it into the Android libs folder.
## Requires: go, gomobile, and Android NDK (set ANDROID_NDK_HOME).
##   go install golang.org/x/mobile/cmd/gomobile@latest
##   gomobile init
gomobile: $(AAR)

$(AAR): $(shell find $(STCLIENT) -name '*.go')
	mkdir -p android/app/libs
	cd $(STCLIENT) && $(GOMOBILE) bind \
		-target android \
		-androidapi 21 \
		-javapkg com.acidtv.unsyncthing \
		-ldflags="-extldflags=-Wl,-z,max-page-size=16384" \
		-o ../$(AAR) \
		.

## Run Go unit tests.
test:
	cd $(STCLIENT) && go test ./...

## Run `go mod tidy` inside stclient/ to populate go.sum.
tidy:
	cd $(STCLIENT) && go mod tidy

## Build a debug APK (requires gomobile AAR to exist first).
android: $(AAR)
	cd android && ./gradlew assembleDebug

## Install directly to a connected device.
install: android
	cd android && ./gradlew installDebug

clean:
	rm -f $(AAR)
	cd android && ./gradlew clean 2>/dev/null || true
