FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN go build ./...
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -o /out/grainfumigate ./cmd/grainfumigate

FROM golang:1.25-bookworm
WORKDIR /app
COPY --from=build /out/grainfumigate /usr/local/bin/grainfumigate
EXPOSE 8080
CMD ["/usr/local/bin/grainfumigate", "serve"]
