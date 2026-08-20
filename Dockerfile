FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /out/controller ./cmd/controller && \
    CGO_ENABLED=0 go build -o /out/mockworker ./cmd/mockworker && \
    CGO_ENABLED=0 go build -o /out/loadgen ./cmd/loadgen

FROM alpine:3.20
COPY --from=build /out/ /usr/local/bin/
