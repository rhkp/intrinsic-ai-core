FROM golang:1.25.4-alpine3.22 AS build
RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/chart-assignment-controller ./cmd/chart-assignment-controller

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/chart-assignment-controller /usr/local/bin/chart-assignment-controller
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/chart-assignment-controller"]
